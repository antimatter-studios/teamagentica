package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-veo/internal/usage"
	"github.com/antimatter-studios/teamagentica/plugins/agent-veo/internal/veo"
)

// rehostToStorage downloads a remote URL and writes it to object storage under
// the given key. Returns a storage:// URL on success. On failure (network,
// storage write, no sdk), returns the original URL unchanged.
func (h *Handler) rehostToStorage(ctx context.Context, externalURL, key, mimeType string) string {
	if h.sdk == nil || externalURL == "" {
		return externalURL
	}
	dlCtx, cancelDL := context.WithTimeout(ctx, 120*time.Second)
	defer cancelDL()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, externalURL, nil)
	if err != nil {
		log.Printf("rehost: build request for %s: %v", externalURL, err)
		return externalURL
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("rehost: download %s: %v", externalURL, err)
		return externalURL
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("rehost: download %s returned %d", externalURL, resp.StatusCode)
		return externalURL
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("rehost: read body %s: %v", externalURL, err)
		return externalURL
	}
	writeCtx, cancelW := context.WithTimeout(ctx, 60*time.Second)
	defer cancelW()
	if err := h.sdk.StorageWrite(writeCtx, key, bytes.NewReader(body), mimeType); err != nil {
		log.Printf("rehost: storage write %s: %v", key, err)
		return externalURL
	}
	return "storage://" + key
}

// task tracks an in-flight video generation.
type task struct {
	ID            string    `json:"id"`
	Prompt        string    `json:"prompt"`
	Model         string    `json:"model"`
	OperationName string    `json:"operation_name"`
	Status        string    `json:"status"` // "processing", "completed", "failed"
	VideoURI      string    `json:"video_uri,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
}

type Handler struct {
	mu     sync.RWMutex
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *veo.Client
	usage  *usage.Tracker

	tasks  map[string]*task
	nextID int
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: veo.NewClient(apiKey, model, debug),
		usage:  usage.NewTracker(dataPath),
		tasks:  make(map[string]*task),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	changed := false
	if v, ok := config["GEMINI_API_KEY"]; ok && v != h.apiKey {
		log.Printf("[config] updating API key")
		h.apiKey = v
		changed = true
	}
	if v, ok := config["VEO_MODEL"]; ok && v != "" && v != h.model {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
		changed = true
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
		changed = true
	}
	if changed {
		h.client = veo.NewClient(h.apiKey, h.model, h.debug)
	}
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	h.mu.RLock()
	apiKey, model := h.apiKey, h.model
	h.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-veo",
		"version":    "1.0.0",
		"configured": apiKey != "",
		"model":      model,
	})
}

// generateRequest is the body for POST /generate.
type generateRequest struct {
	Prompt         string `json:"prompt" binding:"required"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// ChatStream implements pluginsdk.AgentProvider. Video generation is async;
// this blocks up to ~100s while polling the Veo API. For jobs that exceed the
// deadline, the task record is retained and reachable via GET /status/:taskId.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 2)
	go func() {
		defer close(ch)

		h.mu.RLock()
		apiKey, model, client := h.apiKey, h.model, h.client
		h.mu.RUnlock()

		if apiKey == "" {
			ch <- pluginsdk.StreamError("No API key configured. Set GEMINI_API_KEY.")
			return
		}
		if req.Model != "" {
			model = req.Model
		}

		prompt := req.Message
		for i := len(req.Conversation) - 1; i >= 0; i-- {
			if req.Conversation[i].Role == "user" && req.Conversation[i].Content != "" {
				prompt = req.Conversation[i].Content
				break
			}
		}
		if prompt == "" {
			ch <- pluginsdk.StreamError("message or conversation required")
			return
		}

		h.emitEvent("chat_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(prompt, 100)))
		start := time.Now()

		result, err := client.Generate(veo.GenerateRequest{Prompt: prompt})
		if err != nil {
			log.Printf("Veo chat/generate error: %v", err)
			h.emitEvent("error", fmt.Sprintf("chat: %v", err))
			if h.sdk != nil {
				h.sdk.ReportUsage(pluginsdk.UsageReport{
					UserID: req.UserID, Provider: "veo", Model: model,
					RecordType: "request", Status: "failed",
					Prompt: truncateStr(prompt, 200), DurationMs: time.Since(start).Milliseconds(),
				})
			}
			ch <- pluginsdk.StreamError("Video generation failed: " + err.Error())
			return
		}

		h.mu.Lock()
		h.nextID++
		taskID := fmt.Sprintf("veo-%d", h.nextID)
		h.tasks[taskID] = &task{
			ID: taskID, Prompt: prompt, Model: model,
			OperationName: result.OperationName,
			Status:        "processing",
			CreatedAt:     time.Now().UTC(),
		}
		h.mu.Unlock()

		const pollInterval = 5 * time.Second
		const maxWait = 100 * time.Second
		deadline := time.Now().Add(maxWait)

		var statusResult *veo.StatusResult
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
			statusResult, err = client.CheckStatus(result.OperationName)
			if err != nil {
				log.Printf("Veo poll error: %v", err)
				continue
			}
			if statusResult.Done {
				break
			}
		}

		elapsed := time.Since(start)

		if statusResult == nil || !statusResult.Done {
			ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
				Response: fmt.Sprintf("Video generation is still in progress (task: %s). Check GET /status/%s for progress.", taskID, taskID),
				Model:    model,
			})
			return
		}

		if statusResult.Error != "" {
			h.mu.Lock()
			t := h.tasks[taskID]
			if t != nil {
				t.Status = "failed"
				t.Error = statusResult.Error
				t.CompletedAt = time.Now().UTC()
			}
			h.mu.Unlock()
			ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
				Response: "Video generation failed: " + statusResult.Error,
				Model:    model,
			})
			return
		}

		h.mu.Lock()
		t := h.tasks[taskID]
		if t != nil {
			t.Status = "completed"
			t.VideoURI = statusResult.VideoURI
			t.CompletedAt = time.Now().UTC()
		}
		h.mu.Unlock()

		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID: req.UserID, Provider: "veo", Model: model,
				RecordType: "request", Status: "completed",
				Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
				TaskID: taskID,
			})
		}

		h.emitEvent("chat_response", fmt.Sprintf("task=%s duration=%dms", taskID, elapsed.Milliseconds()))

		rehostKey := fmt.Sprintf(".private/generated/agent-veo/%s.mp4", uuid.NewString())
		finalURL := h.rehostToStorage(ctx, statusResult.VideoURI, rehostKey, "video/mp4")

		ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
			Response: fmt.Sprintf("Here is the generated video (task: %s).", taskID),
			Model:    model,
			Attachments: []pluginsdk.AgentAttachment{{
				Type:     "video",
				MimeType: "video/mp4",
				URL:      finalURL,
				Filename: fmt.Sprintf("veo-%d.mp4", time.Now().Unix()),
			}},
		})
	}()
	return ch
}

// Generate submits a video generation request to Veo.
func (h *Handler) Generate(c *gin.Context) {
	h.mu.RLock()
	apiKey, model, client := h.apiKey, h.model, h.client
	h.mu.RUnlock()

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set GEMINI_API_KEY."})
		return
	}

	var req generateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: prompt is required"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(req.Prompt, 100)))

	result, err := client.Generate(veo.GenerateRequest{
		Prompt:         req.Prompt,
		AspectRatio:    req.AspectRatio,
		NegativePrompt: req.NegativePrompt,
	})
	if err != nil {
		log.Printf("Veo generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("generate: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "Veo request failed: " + err.Error()})
		return
	}

	// Create internal task to track this generation.
	h.mu.Lock()
	h.nextID++
	taskID := fmt.Sprintf("veo-%d", h.nextID)
	t := &task{
		ID:            taskID,
		Prompt:        req.Prompt,
		Model:         model,
		OperationName: result.OperationName,
		Status:        "processing",
		CreatedAt:     time.Now().UTC(),
	}
	h.tasks[taskID] = t
	h.mu.Unlock()

	h.usage.RecordRequest(usage.RequestRecord{
		Model:  model,
		Prompt: truncateStr(req.Prompt, 200),
		TaskID: taskID,
		Status: "submitted",
	})
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:     userID,
			Provider:   "veo",
			Model:      model,
			RecordType: "request",
			Status:     "submitted",
			Prompt:     truncateStr(req.Prompt, 200),
			TaskID:     taskID,
		})
	}

	h.emitEvent("generate_submitted", fmt.Sprintf("task=%s operation=%s", taskID, result.OperationName))

	c.JSON(http.StatusAccepted, gin.H{
		"task_id": taskID,
		"status":  "processing",
		"message": "Video generation started. Poll GET /status/" + taskID + " for progress.",
	})
}

// Status checks the progress of a video generation task.
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

	// If already completed/failed, return cached result.
	if t.Status == "completed" || t.Status == "failed" {
		c.JSON(http.StatusOK, gin.H{
			"task_id":   t.ID,
			"status":    t.Status,
			"video_uri": t.VideoURI,
			"error":     t.Error,
			"model":     t.Model,
			"prompt":    t.Prompt,
			"created_at":   t.CreatedAt,
			"completed_at": t.CompletedAt,
		})
		return
	}

	// Poll the Veo API.
	h.mu.RLock()
	client := h.client
	h.mu.RUnlock()

	statusResult, err := client.CheckStatus(t.OperationName)
	if err != nil {
		log.Printf("Veo status check error: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"task_id": t.ID,
			"status":  "processing",
			"error":   "status check failed: " + err.Error(),
			"model":   t.Model,
			"prompt":  t.Prompt,
		})
		return
	}

	if statusResult.Done {
		h.mu.Lock()
		t.CompletedAt = time.Now().UTC()
		if statusResult.Error != "" {
			t.Status = "failed"
			t.Error = statusResult.Error
		} else {
			t.Status = "completed"
			t.VideoURI = statusResult.VideoURI
		}
		h.mu.Unlock()

		elapsed := t.CompletedAt.Sub(t.CreatedAt)
		h.usage.RecordRequest(usage.RequestRecord{
			Model:      t.Model,
			Prompt:     truncateStr(t.Prompt, 200),
			TaskID:     t.ID,
			Status:     t.Status,
			DurationMs: elapsed.Milliseconds(),
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:     userID,
				Provider:   "veo",
				Model:      t.Model,
				RecordType: "request",
				Status:     t.Status,
				Prompt:     truncateStr(t.Prompt, 200),
				TaskID:     t.ID,
				DurationMs: elapsed.Milliseconds(),
			})
		}

		h.emitEvent("generate_complete", fmt.Sprintf("task=%s status=%s duration=%dms", t.ID, t.Status, elapsed.Milliseconds()))
	}

	c.JSON(http.StatusOK, gin.H{
		"task_id":   t.ID,
		"status":    t.Status,
		"video_uri": t.VideoURI,
		"error":     t.Error,
		"model":     t.Model,
		"prompt":    t.Prompt,
		"created_at":   t.CreatedAt,
		"completed_at": t.CompletedAt,
	})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	h.mu.RLock()
	apiKey, client := h.apiKey, h.client
	h.mu.RUnlock()

	switch field {
	case "VEO_MODEL":
		if apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": defaultModels(), "fallback": true})
			return
		}
		models, err := client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
			c.JSON(http.StatusOK, gin.H{"options": defaultModels(), "fallback": true, "error": "Failed to fetch models: " + err.Error()})
			return
		}
		if len(models) == 0 {
			c.JSON(http.StatusOK, gin.H{"options": defaultModels(), "fallback": true})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": models, "fallback": false})
	default:
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Unknown field"})
	}
}

// Usage returns accumulated usage stats.
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

// SystemPrompt returns the system prompt this plugin would use when processing requests.
func (h *Handler) SystemPrompt(c *gin.Context) {
	h.mu.RLock()
	model := h.model
	h.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"system_prompt": buildSystemPrompt(model),
	})
}

func buildSystemPrompt(model string) string {
	return fmt.Sprintf(`You are a video generation tool powered by Google Gemini Veo.

CAPABILITIES:
- Generate videos from text prompts
- Current model: %s
- Asynchronous generation (returns task ID, poll for completion)
- Configurable aspect ratio
- Negative prompt support

PARAMETERS:
- prompt (required): Text description of the video to generate
- aspect_ratio (optional): Video aspect ratio
- negative_prompt (optional): What to exclude from the video

OUTPUT:
- Returns task ID immediately
- Poll status endpoint for completion
- Final result includes video URI

GUIDELINES:
- Video generation is asynchronous — always inform the user that generation is in progress
- Be descriptive and specific in prompt interpretation
- Report any generation failures clearly`, model)
}

// ToolDefs returns the raw tool definitions for this plugin.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "generate_video",
			"description": "Generate a video from a text prompt using Google Veo. Returns the video synchronously. Use for short, prompt-based video generation.",
			"endpoint":    "/generate",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prompt":          gin.H{"type": "string", "description": "Text prompt describing the video to generate"},
					"aspect_ratio":    gin.H{"type": "string", "description": "Video aspect ratio"},
					"negative_prompt": gin.H{"type": "string", "description": "What to exclude from the video"},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

// Tools returns the available tool schemas for this plugin.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

func defaultModels() []string {
	return []string{
		"veo-3.1-generate-preview",
		"veo-3-generate-preview",
	}
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
