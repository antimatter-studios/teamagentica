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
	"github.com/antimatter-studios/teamagentica/plugins/agent-seedance/internal/seedance"
	"github.com/antimatter-studios/teamagentica/plugins/agent-seedance/internal/usage"
)

// rehostToStorage downloads a remote URL and writes it to object storage under
// the given key. Returns a storage:// URL on success. On failure (network,
// storage write, no sdk), returns the original URL unchanged — external
// provider URLs usually work immediately; rehosting is durability insurance.
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
	apiKey          string
	debug           bool
	sdk             *pluginsdk.Client
	client          *seedance.Client
	usage           *usage.Tracker
	callbackBaseURL string

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

// SetCallbackBaseURL stores the public webhook URL for use as callback_url in generate requests.
func (h *Handler) SetCallbackBaseURL(url string) {
	h.mu.Lock()
	h.callbackBaseURL = url
	h.mu.Unlock()
	log.Printf("[webhook] callback base URL set: %s", url)
}

// WebhookCallback handles async status notifications from the Seedance API.
// The ingress proxies POST /agent-seedance/callback/:taskId → POST /callback/:taskId.
func (h *Handler) WebhookCallback(c *gin.Context) {
	// Debug probe: curl with X-Webhook-Debug header to verify ngrok forwarding.
	if c.GetHeader("X-Webhook-Debug") != "" {
		log.Printf("[webhook] debug probe received for path %s from %s", c.Request.URL.Path, c.ClientIP())
		c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "webhook endpoint reachable", "path": c.Request.URL.Path})
		return
	}

	taskID := c.Param("taskId")

	h.mu.RLock()
	t, exists := h.tasks[taskID]
	h.mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	// If already terminal, ignore duplicate callbacks.
	if t.Status == "completed" || t.Status == "failed" {
		c.JSON(http.StatusOK, gin.H{"status": "already_terminal"})
		return
	}

	// Poll the Seedance API for the latest status (the callback is a notification,
	// actual status comes from the status endpoint).
	statusResult, err := h.client.CheckStatus(t.RemoteID)
	if err != nil {
		log.Printf("[webhook] status check error for task %s: %v", taskID, err)
		c.JSON(http.StatusOK, gin.H{"status": "poll_error"})
		return
	}

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

		// Report progress to relay via event bus — rehost video to our storage
		// so Discord etc. can fetch it even after the provider URL expires.
		if h.sdk != nil {
			rehostKey := fmt.Sprintf(".private/generated/agent-seedance/%s.mp4", uuid.NewString())
			finalURL := h.rehostToStorage(context.Background(), statusResult.VideoURL, rehostKey, "video/mp4")
			h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
				TaskID:  taskID,
				Status:  "completed",
				Message: fmt.Sprintf("Video generation complete (%.0fs)", elapsed.Seconds()),
				Attachments: []struct {
					Type     string `json:"type,omitempty"`
					MimeType string `json:"mime_type,omitempty"`
					URL      string `json:"url,omitempty"`
					Filename string `json:"filename,omitempty"`
				}{
					{Type: "video", MimeType: "video/mp4", URL: finalURL, Filename: fmt.Sprintf("seedance-%d.mp4", time.Now().Unix())},
				},
			})
		}

		h.emitEvent("generate_complete", fmt.Sprintf("task=%s duration=%dms (webhook)", taskID, elapsed.Milliseconds()))

	case "failed":
		h.mu.Lock()
		t.Status = "failed"
		t.Error = statusResult.Error
		t.CompletedAt = time.Now().UTC()
		h.mu.Unlock()

		if h.sdk != nil {
			h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
				TaskID:  taskID,
				Status:  "failed",
				Message: "Video generation failed: " + statusResult.Error,
			})
		}

		h.emitEvent("generate_failed", fmt.Sprintf("task=%s error=%s (webhook)", taskID, statusResult.Error))

	default:
		// Still processing — report progress.
		if h.sdk != nil {
			elapsed := time.Since(t.CreatedAt)
			h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
				TaskID:  taskID,
				Status:  "processing",
				Message: fmt.Sprintf("Video generating... (%.0fs elapsed)", elapsed.Seconds()),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// pollUntilComplete is a background fallback that polls the Seedance status API
// continuously while the task is in progress. The webhook is the primary delivery
// mechanism; this loop ensures we eventually resolve even if the webhook fails.
// There is no fixed timeout — as long as the API says "processing", we keep waiting.
// Only consecutive errors cause the loop to give up (max 5 in a row).
func (h *Handler) pollUntilComplete(taskID, remoteID string, start time.Time) {
	const pollInterval = 30 * time.Second
	const maxConsecutiveErrors = 5

	// Wait before first poll — give the webhook a chance to arrive.
	time.Sleep(pollInterval)

	consecutiveErrors := 0
	pollCount := 0

	for {
		h.mu.RLock()
		t, exists := h.tasks[taskID]
		if !exists {
			h.mu.RUnlock()
			return
		}
		status := t.Status
		h.mu.RUnlock()

		// Webhook already resolved this task — nothing to do.
		if status == "completed" || status == "failed" {
			log.Printf("[poll] task %s already %s — stopping background poller", taskID, status)
			return
		}

		pollCount++
		log.Printf("[poll] checking status for task %s (poll #%d, %.0fs elapsed)", taskID, pollCount, time.Since(start).Seconds())
		statusResult, err := h.client.CheckStatus(remoteID)
		if err != nil {
			consecutiveErrors++
			log.Printf("[poll] status check error for task %s (%d/%d): %v", taskID, consecutiveErrors, maxConsecutiveErrors, err)
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Printf("[poll] task %s: giving up after %d consecutive errors", taskID, maxConsecutiveErrors)
				if h.sdk != nil {
					h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
						TaskID:  taskID,
						Status:  "failed",
						Message: fmt.Sprintf("Video generation status check failed after %d consecutive errors: %v", maxConsecutiveErrors, err),
					})
				}
				h.mu.Lock()
				t.Status = "failed"
				t.Error = fmt.Sprintf("status check failed: %v", err)
				t.CompletedAt = time.Now().UTC()
				h.mu.Unlock()
				return
			}
			time.Sleep(pollInterval)
			continue
		}
		consecutiveErrors = 0

		switch statusResult.Status {
		case "completed":
			h.mu.Lock()
			if t.Status == "completed" || t.Status == "failed" {
				h.mu.Unlock()
				log.Printf("[poll] task %s resolved by webhook while polling — stopping", taskID)
				return
			}
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
				rehostKey := fmt.Sprintf(".private/generated/agent-seedance/%s.mp4", uuid.NewString())
				finalURL := h.rehostToStorage(context.Background(), statusResult.VideoURL, rehostKey, "video/mp4")
				h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
					TaskID:  taskID,
					Status:  "completed",
					Message: fmt.Sprintf("Video generation complete (%.0fs, poll fallback)", elapsed.Seconds()),
					Attachments: []struct {
						Type     string `json:"type,omitempty"`
						MimeType string `json:"mime_type,omitempty"`
						URL      string `json:"url,omitempty"`
						Filename string `json:"filename,omitempty"`
					}{
						{Type: "video", MimeType: "video/mp4", URL: finalURL, Filename: fmt.Sprintf("seedance-%d.mp4", time.Now().Unix())},
					},
				})
			}

			h.emitEvent("generate_complete", fmt.Sprintf("task=%s duration=%dms (poll)", taskID, elapsed.Milliseconds()))
			return

		case "failed":
			h.mu.Lock()
			if t.Status == "completed" || t.Status == "failed" {
				h.mu.Unlock()
				return
			}
			t.Status = "failed"
			t.Error = statusResult.Error
			t.CompletedAt = time.Now().UTC()
			h.mu.Unlock()

			if h.sdk != nil {
				h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
					TaskID:  taskID,
					Status:  "failed",
					Message: "Video generation failed: " + statusResult.Error,
				})
			}

			h.emitEvent("generate_failed", fmt.Sprintf("task=%s error=%s (poll)", taskID, statusResult.Error))
			return

		default:
			// Still processing — report progress and keep polling.
			if h.sdk != nil {
				elapsed := time.Since(start)
				h.sdk.ReportRelayProgress(pluginsdk.ProgressUpdate{
					TaskID:  taskID,
					Status:  "processing",
					Message: fmt.Sprintf("Video generating... (%.0fs elapsed)", elapsed.Seconds()),
				})
			}
		}

		time.Sleep(pollInterval)
	}
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	changed := false
	if v, ok := config["SEEDANCE_API_KEY"]; ok && v != h.apiKey {
		log.Printf("[config] updating API key")
		h.apiKey = v
		changed = true
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
		changed = true
	}
	if changed {
		h.client = seedance.NewClient(h.apiKey, h.debug)
	}
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	h.mu.RLock()
	apiKey := h.apiKey
	h.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-seedance",
		"version":    "1.0.0",
		"configured": apiKey != "",
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
	h.mu.RLock()
	apiKey, client := h.apiKey, h.client
	h.mu.RUnlock()

	if apiKey == "" {
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

	result, err := client.Generate(seedance.GenerateRequest{
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

func (h *Handler) Models(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"models": []string{"seedance-2.0"}})
}

// ChatStream implements pluginsdk.AgentProvider. Video generation is async;
// this blocks up to ~100s while polling the Seedance API. For longer jobs,
// the webhook-driven async path (relay:task:progress events) remains active
// via /callback/:taskId.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 2)
	go func() {
		defer close(ch)

		h.mu.RLock()
		apiKey, client, callbackBase := h.apiKey, h.client, h.callbackBaseURL
		h.mu.RUnlock()

		if apiKey == "" {
			ch <- pluginsdk.StreamError("No API key configured. Set SEEDANCE_API_KEY.")
			return
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

		h.emitEvent("chat_request", fmt.Sprintf("prompt=%s", truncateStr(prompt, 100)))
		start := time.Now()

		h.mu.Lock()
		h.nextID++
		taskID := fmt.Sprintf("seed-%d", h.nextID)
		h.mu.Unlock()

		genReq := seedance.GenerateRequest{Prompt: prompt}
		if callbackBase != "" {
			genReq.CallbackURL = callbackBase + "/callback/" + taskID
		}

		result, err := client.Generate(genReq)
		if err != nil {
			log.Printf("Seedance chat/generate error: %v", err)
			if h.sdk != nil {
				h.sdk.ReportUsage(pluginsdk.UsageReport{
					UserID: req.UserID, Provider: "seedance", Model: "seedance-2.0",
					RecordType: "request", Status: "failed",
					Prompt: truncateStr(prompt, 200), DurationMs: time.Since(start).Milliseconds(),
				})
			}
			ch <- pluginsdk.StreamError("Video generation failed: " + err.Error())
			return
		}

		h.mu.Lock()
		h.tasks[taskID] = &task{
			ID: taskID, Prompt: prompt, RemoteID: result.TaskID,
			Status: "processing", CreatedAt: time.Now().UTC(),
		}
		h.mu.Unlock()

		// Poll until complete, failed, or deadline.
		const pollInterval = 5 * time.Second
		const maxWait = 100 * time.Second
		deadline := time.Now().Add(maxWait)

		var status *seedance.StatusResult
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
			status, err = client.CheckStatus(result.TaskID)
			if err != nil {
				log.Printf("Seedance poll error: %v", err)
				continue
			}
			if status.Status == "completed" || status.Status == "failed" {
				break
			}
		}

		elapsed := time.Since(start)

		if status == nil || status.Status != "completed" {
			if status != nil && status.Status == "failed" {
				errMsg := status.Error
				if errMsg == "" {
					errMsg = "unknown error"
				}
				if h.sdk != nil {
					h.sdk.ReportUsage(pluginsdk.UsageReport{
						UserID: req.UserID, Provider: "seedance", Model: "seedance-2.0",
						RecordType: "request", Status: "failed",
						Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
					})
				}
				ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
					Response: "Video generation failed: " + errMsg,
					Model:    "seedance-2.0",
				})
				return
			}
			// Still processing after deadline — webhook path will deliver later.
			// Background poller ensures the task record gets finalised.
			go h.pollUntilComplete(taskID, result.TaskID, start)
			ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
				Response: fmt.Sprintf("Video generation is still in progress (task: %s). It typically takes 2-4 minutes. The video will be delivered once processing completes.", result.TaskID),
				Model:    "seedance-2.0",
			})
			return
		}

		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID: req.UserID, Provider: "seedance", Model: "seedance-2.0",
				RecordType: "request", Status: "completed",
				Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
			})
		}

		h.emitEvent("chat_response", fmt.Sprintf("duration=%dms", elapsed.Milliseconds()))

		rehostKey := fmt.Sprintf(".private/generated/agent-seedance/%s.mp4", uuid.NewString())
		finalURL := h.rehostToStorage(ctx, status.VideoURL, rehostKey, "video/mp4")

		ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
			Response: fmt.Sprintf("Here is the generated video (task: %s).", taskID),
			Model:    "seedance-2.0",
			Attachments: []pluginsdk.AgentAttachment{{
				Type:     "video",
				MimeType: "video/mp4",
				URL:      finalURL,
				Filename: fmt.Sprintf("seedance-%d.mp4", time.Now().Unix()),
			}},
		})
	}()
	return ch
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

// SystemPrompt returns the system prompt this plugin would use when processing requests.
func (h *Handler) SystemPrompt(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"system_prompt": buildSystemPrompt(),
	})
}

func buildSystemPrompt() string {
	return `You are a video generation tool powered by Seedance API (seedance-2.0 model).

CAPABILITIES:
- Generate videos from text prompts
- Asynchronous generation (returns task ID, poll for completion)
- Optional audio generation
- Optional reference image input
- Configurable aspect ratio, resolution, and duration

PARAMETERS:
- prompt (required): Text description of the video to generate
- aspect_ratio (optional): Video aspect ratio
- resolution (optional): Output resolution
- duration (optional): Video duration
- generate_audio (optional): Generate audio track
- fixed_lens (optional): Use fixed camera lens
- image_urls (optional): Reference images for guided generation

OUTPUT:
- Returns task ID immediately
- Poll status endpoint for completion
- Final result includes video URL

GUIDELINES:
- Video generation is asynchronous — the /chat endpoint polls for up to ~100s
- If the video is still processing when /chat returns, the response will include a task ID
- Use check_video_status with that task ID to retrieve the video URL later
- Be descriptive and specific in prompt interpretation
- Report any generation failures clearly`
}

// ToolDefs returns the raw tool definitions for use by both the HTTP handler and the SDK registration.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "generate_video",
			"description": "Generate a video from a text prompt using Seedance. Async — returns a task_id; you must call check_video_status to poll for the result.",
			"endpoint":    "/generate",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prompt":         gin.H{"type": "string", "description": "Text prompt describing the video to generate"},
					"aspect_ratio":   gin.H{"type": "string", "description": "Video aspect ratio (1:1, 16:9, 9:16, 4:3, 3:4, 21:9, 9:21)"},
					"resolution":     gin.H{"type": "string", "description": "Output resolution: 480p or 720p", "enum": []string{"480p", "720p"}},
					"duration":       gin.H{"type": "string", "description": "Video duration in seconds: 4, 8, or 12", "enum": []string{"4", "8", "12"}},
					"generate_audio": gin.H{"type": "boolean", "description": "Whether to generate audio"},
					"fixed_lens":     gin.H{"type": "boolean", "description": "Use fixed camera lens"},
				},
				"required": []string{"prompt"},
			},
		},
		{
			"name":        "check_video_status",
			"description": "Poll a Seedance video generation task. Returns status and video URL when complete. Must be called after generate_video — Seedance is async.",
			"endpoint":    "/status/:taskId",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"task_id": gin.H{"type": "string", "description": "The task ID returned from generate_video"},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

// Tools returns the available tool schemas for this plugin.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
