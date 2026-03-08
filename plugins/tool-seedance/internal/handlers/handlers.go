package handlers

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-seedance/internal/seedance"
	"github.com/antimatter-studios/teamagentica/plugins/tool-seedance/internal/usage"
)

// task tracks an in-flight video generation.
type task struct {
	ID          string    `json:"id"`
	Prompt      string    `json:"prompt"`
	RemoteID    string    `json:"remote_id"` // Seedance task_id
	Status      string    `json:"status"`    // "processing", "completed", "failed"
	VideoURL    string    `json:"video_url,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type Handler struct {
	apiKey string
	debug  bool
	sdk    *pluginsdk.Client
	client *seedance.Client
	usage  *usage.Tracker

	mu     sync.RWMutex
	tasks  map[string]*task
	nextID int
}

func NewHandler(apiKey, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		debug:  debug,
		client: seedance.NewClient(apiKey, debug),
		usage:  usage.NewTracker(dataPath),
		tasks:  make(map[string]*task),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "tool-seedance",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
	})
}

type generateRequest struct {
	Prompt        string   `json:"prompt" binding:"required"`
	AspectRatio   string   `json:"aspect_ratio,omitempty"`
	Resolution    string   `json:"resolution,omitempty"`
	Duration      string   `json:"duration,omitempty"`
	GenerateAudio bool     `json:"generate_audio,omitempty"`
	FixedLens     bool     `json:"fixed_lens,omitempty"`
	ImageURLs     []string `json:"image_urls,omitempty"`
}

func (h *Handler) Generate(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set SEEDANCE_API_KEY."})
		return
	}

	var req generateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: prompt is required"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("prompt=%s", truncateStr(req.Prompt, 100)))

	result, err := h.client.Generate(seedance.GenerateRequest{
		Prompt:        req.Prompt,
		AspectRatio:   req.AspectRatio,
		Resolution:    req.Resolution,
		Duration:      req.Duration,
		GenerateAudio: req.GenerateAudio,
		FixedLens:     req.FixedLens,
		ImageURLs:     req.ImageURLs,
	})
	if err != nil {
		log.Printf("Seedance generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("generate: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "Seedance request failed: " + err.Error()})
		return
	}

	// Create internal task.
	h.mu.Lock()
	h.nextID++
	taskID := fmt.Sprintf("seed-%d", h.nextID)
	t := &task{
		ID:        taskID,
		Prompt:    req.Prompt,
		RemoteID:  result.TaskID,
		Status:    "processing",
		CreatedAt: time.Now().UTC(),
	}
	h.tasks[taskID] = t
	h.mu.Unlock()

	h.usage.RecordRequest(usage.RequestRecord{
		Model:  "seedance-2.0",
		Prompt: truncateStr(req.Prompt, 200),
		TaskID: taskID,
		Status: "submitted",
	})
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:     userID,
			Provider:   "seedance",
			Model:      "seedance-2.0",
			RecordType: "request",
			Status:     "submitted",
			Prompt:     truncateStr(req.Prompt, 200),
			TaskID:     taskID,
		})
	}

	h.emitEvent("generate_submitted", fmt.Sprintf("task=%s remote=%s", taskID, result.TaskID))

	c.JSON(http.StatusAccepted, gin.H{
		"task_id": taskID,
		"status":  "processing",
		"message": "Video generation started. Poll GET /status/" + taskID + " for progress.",
	})
}

func (h *Handler) Status(c *gin.Context) {
	userID := c.Request.Header.Get("X-Teamagentica-User-ID")
	taskID := c.Param("taskId")

	h.mu.RLock()
	t, exists := h.tasks[taskID]
	h.mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// Return cached result if terminal.
	if t.Status == "completed" || t.Status == "failed" {
		c.JSON(http.StatusOK, gin.H{
			"task_id":      t.ID,
			"status":       t.Status,
			"video_url":    t.VideoURL,
			"error":        t.Error,
			"prompt":       t.Prompt,
			"created_at":   t.CreatedAt,
			"completed_at": t.CompletedAt,
		})
		return
	}

	// Poll Seedance API.
	statusResult, err := h.client.CheckStatus(t.RemoteID)
	if err != nil {
		log.Printf("Seedance status check error: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"task_id": t.ID,
			"status":  "processing",
			"error":   "status check failed: " + err.Error(),
			"prompt":  t.Prompt,
		})
		return
	}

	// Map Seedance statuses to our statuses.
	switch statusResult.Status {
	case "completed":
		h.mu.Lock()
		t.Status = "completed"
		t.VideoURL = statusResult.VideoURL
		t.CompletedAt = time.Now().UTC()
		h.mu.Unlock()

		elapsed := t.CompletedAt.Sub(t.CreatedAt)
		h.usage.RecordRequest(usage.RequestRecord{
			Model:      "seedance-2.0",
			Prompt:     truncateStr(t.Prompt, 200),
			TaskID:     t.ID,
			Status:     "completed",
			DurationMs: elapsed.Milliseconds(),
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:     userID,
				Provider:   "seedance",
				Model:      "seedance-2.0",
				RecordType: "request",
				Status:     "completed",
				Prompt:     truncateStr(t.Prompt, 200),
				TaskID:     t.ID,
				DurationMs: elapsed.Milliseconds(),
			})
		}
		h.emitEvent("generate_complete", fmt.Sprintf("task=%s duration=%dms", t.ID, elapsed.Milliseconds()))

	case "failed":
		h.mu.Lock()
		t.Status = "failed"
		t.Error = statusResult.Error
		t.CompletedAt = time.Now().UTC()
		h.mu.Unlock()

		h.usage.RecordRequest(usage.RequestRecord{
			Model:  "seedance-2.0",
			Prompt: truncateStr(t.Prompt, 200),
			TaskID: t.ID,
			Status: "failed",
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:     userID,
				Provider:   "seedance",
				Model:      "seedance-2.0",
				RecordType: "request",
				Status:     "failed",
				Prompt:     truncateStr(t.Prompt, 200),
				TaskID:     t.ID,
			})
		}
		h.emitEvent("generate_failed", fmt.Sprintf("task=%s error=%s", t.ID, statusResult.Error))
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":      t.ID,
		"status":       t.Status,
		"video_url":    t.VideoURL,
		"error":        t.Error,
		"prompt":       t.Prompt,
		"created_at":   t.CreatedAt,
		"completed_at": t.CompletedAt,
	})
}

func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "SEEDANCE_MODEL":
		c.JSON(http.StatusOK, gin.H{"options": []string{"seedance-2.0"}})
	default:
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Unknown field"})
	}
}

func (h *Handler) Usage(c *gin.Context) {
	c.JSON(http.StatusOK, h.usage.Summary())
}

// UsageRecords returns raw request records, optionally filtered by ?since=RFC3339.
func (h *Handler) UsageRecords(c *gin.Context) {
	since := c.Query("since")
	records := h.usage.Records(since)
	if records == nil {
		records = []usage.RequestRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
