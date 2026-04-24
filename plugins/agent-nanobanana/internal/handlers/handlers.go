package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-nanobanana/internal/nanobanana"
	"github.com/antimatter-studios/teamagentica/plugins/agent-nanobanana/internal/usage"
)

type Handler struct {
	mu     sync.RWMutex
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *nanobanana.Client
	usage  *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: nanobanana.NewClient(apiKey, model, debug),
		usage:  usage.NewTracker(dataPath),
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
	if v, ok := config["NANOBANANA_MODEL"]; ok && v != "" && v != h.model {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
		changed = true
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
		changed = true
	}
	if changed {
		h.client = nanobanana.NewClient(h.apiKey, h.model, h.debug)
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
		"status":        "ok",
		"plugin":        "agent-nanobanana",
		"version":       "1.0.0",
		"configured":    apiKey != "",
		"default_model": model,
	})
}

func (h *Handler) Models(c *gin.Context) {
	h.mu.RLock()
	apiKey, client := h.apiKey, h.client
	h.mu.RUnlock()

	if apiKey == "" {
		c.JSON(http.StatusOK, gin.H{"models": []string{}})
		return
	}
	models, err := client.ListModels()
	if err != nil {
		log.Printf("ListModels error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

type generateRequest struct {
	Prompt      string   `json:"prompt" binding:"required"`
	Model       string   `json:"model,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
	ImageURLs   []string `json:"image_urls,omitempty"`
}

// Generate creates an image from a text prompt. Unlike video tools, this is
// synchronous — the image data is returned directly in the response.
func (h *Handler) Generate(c *gin.Context) {
	h.mu.RLock()
	apiKey, defaultModel, client := h.apiKey, h.model, h.client
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

	model := req.Model
	if model == "" {
		model = defaultModel
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(req.Prompt, 100)))

	start := time.Now()

	result, err := client.Generate(nanobanana.GenerateRequest{
		Prompt:      req.Prompt,
		Model:       model,
		AspectRatio: req.AspectRatio,
		ImageURLs:   req.ImageURLs,
	})
	if err != nil {
		log.Printf("Nano Banana generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("generate: %v", err))

		h.usage.RecordRequest(usage.RequestRecord{
			Model:      model,
			Prompt:     truncateStr(req.Prompt, 200),
			Status:     "failed",
			DurationMs: time.Since(start).Milliseconds(),
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:     userID,
				Provider:   "nanobanana",
				Model:      model,
				RecordType: "request",
				Status:     "failed",
				Prompt:     truncateStr(req.Prompt, 200),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}

		c.JSON(http.StatusBadGateway, gin.H{"error": "Image generation failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:      model,
		Prompt:     truncateStr(req.Prompt, 200),
		Status:     "completed",
		DurationMs: elapsed.Milliseconds(),
	})
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:     userID,
			Provider:   "nanobanana",
			Model:      model,
			RecordType: "request",
			Status:     "completed",
			Prompt:     truncateStr(req.Prompt, 200),
			DurationMs: elapsed.Milliseconds(),
		})
	}

	attachments, err := h.storeGeneratedImages(c.Request.Context(), result.Images)
	if err != nil {
		log.Printf("Nano Banana store error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Image storage failed: " + err.Error()})
		return
	}

	h.emitEvent("generate_complete", fmt.Sprintf("model=%s duration=%dms count=%d", model, elapsed.Milliseconds(), len(attachments)))

	c.JSON(http.StatusOK, gin.H{
		"status":      "completed",
		"attachments": attachments,
		"count":       len(attachments),
		"text":        result.Text,
		"model":       model,
		"prompt":      req.Prompt,
	})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	h.mu.RLock()
	apiKey, client := h.apiKey, h.client
	h.mu.RUnlock()

	switch field {
	case "NANOBANANA_MODEL":
		if apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured"})
			return
		}
		models, err := client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": models})
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

// ChatStream implements pluginsdk.AgentProvider — the SDK handles SSE framing,
// this method just produces the one-shot image-generation event stream.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 2)
	go func() {
		defer close(ch)

		h.mu.RLock()
		apiKey, defaultModel, client := h.apiKey, h.model, h.client
		h.mu.RUnlock()

		if apiKey == "" {
			ch <- pluginsdk.StreamError("No API key configured. Set GEMINI_API_KEY.")
			return
		}

		model := req.Model
		if model == "" {
			model = defaultModel
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

		result, err := client.Generate(nanobanana.GenerateRequest{Prompt: prompt, Model: model, ImageURLs: req.ImageURLs})
		if err != nil {
			log.Printf("Nano Banana chat/generate error: %v", err)
			h.emitEvent("error", fmt.Sprintf("chat: %v", err))
			if h.sdk != nil {
				h.sdk.ReportUsage(pluginsdk.UsageReport{
					UserID: req.UserID, Provider: "nanobanana", Model: model,
					RecordType: "request", Status: "failed",
					Prompt: truncateStr(prompt, 200), DurationMs: time.Since(start).Milliseconds(),
				})
			}
			ch <- pluginsdk.StreamError("Image generation failed: " + err.Error())
			return
		}

		elapsed := time.Since(start)
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID: req.UserID, Provider: "nanobanana", Model: model,
				RecordType: "request", Status: "completed",
				Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
			})
		}

		responseText := "Here is the generated image."
		if result.Text != "" {
			responseText = result.Text
		}

		h.emitEvent("chat_response", fmt.Sprintf("model=%s duration=%dms count=%d", model, elapsed.Milliseconds(), len(result.Images)))

		attachments, storeErr := h.storeGeneratedImages(ctx, result.Images)
		if storeErr != nil {
			ch <- pluginsdk.StreamError(storeErr.Error())
			return
		}

		ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
			Response:    responseText,
			Model:       model,
			Attachments: attachments,
		})
	}()
	return ch
}

// storeGeneratedImages writes each image to object storage and returns an
// attachment per image. storage:api is a hard dependency — if any write fails
// the error is surfaced rather than silently inlining multi-MB base64.
func (h *Handler) storeGeneratedImages(ctx context.Context, images []nanobanana.GeneratedImage) ([]pluginsdk.AgentAttachment, error) {
	out := make([]pluginsdk.AgentAttachment, 0, len(images))
	now := time.Now().Unix()
	for i, img := range images {
		ext := mimeToExt(img.MimeType)
		key := fmt.Sprintf(".private/generated/agent-nanobanana/%s.%s", uuid.NewString(), ext)
		rawImage, decodeErr := base64.StdEncoding.DecodeString(img.Data)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode image %d: %w", i, decodeErr)
		}
		writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := h.sdk.StorageWrite(writeCtx, key, bytes.NewReader(rawImage), img.MimeType)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("store image %d: %w", i, err)
		}
		out = append(out, pluginsdk.AgentAttachment{
			MimeType: img.MimeType,
			Type:     "image",
			URL:      "storage://" + key,
			Filename: fmt.Sprintf("nanobanana-%d-%d.%s", now, i, ext),
		})
	}
	return out, nil
}

func mimeToExt(mime string) string {
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

// ToolDefs returns the raw tool definitions for this plugin.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "generate_image",
			"description": "Generate a high-quality image using Nano Banana (Gemini image model). Returns an attachments array (usually one image, occasionally more) and a count field — describe exactly that number of images, do not invent additional variants. Preferred image generator — better results than Stability AI. Use this by default unless cost is a concern. Supply image_urls to edit/restyle/compose existing images.",
			"endpoint":    "/generate",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prompt":       gin.H{"type": "string", "description": "Text prompt describing the image to generate"},
					"aspect_ratio": gin.H{"type": "string", "description": "Aspect ratio (1:1, 16:9, 9:16)", "enum": []string{"1:1", "16:9", "9:16"}},
					"image_urls":   gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Optional reference image URLs used as input (image-to-image, editing, style transfer)"},
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
	return fmt.Sprintf(`You are an image generation tool powered by Nano Banana (Google Gemini image models).

CAPABILITIES:
- Generate images from text prompts
- Current model: %s
- Supported aspect ratios: 1:1, 16:9, 9:16

PARAMETERS:
- prompt (required): Text description of the image to generate
- aspect_ratio (optional): Image aspect ratio (default: 1:1)

OUTPUT:
- Returns an attachments array of stored image URLs with a count field
- Only describe the number of images you actually see in count — do not invent variants

GUIDELINES:
- Be descriptive and specific in prompt interpretation
- If the request is ambiguous, generate the most likely intended image
- Report any generation failures clearly`, model)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
