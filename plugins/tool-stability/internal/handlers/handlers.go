package handlers

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-stability/internal/stability"
	"github.com/antimatter-studios/teamagentica/plugins/tool-stability/internal/usage"
)

type Handler struct {
	mu     sync.RWMutex
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *stability.Client
	usage  *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: stability.NewClient(apiKey, model, debug),
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
	if v, ok := config["STABILITY_API_KEY"]; ok && v != h.apiKey {
		log.Printf("[config] updating API key")
		h.apiKey = v
		changed = true
	}
	if v, ok := config["STABILITY_MODEL"]; ok && v != "" && v != h.model {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
		changed = true
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
		changed = true
	}
	if changed {
		h.client = stability.NewClient(h.apiKey, h.model, h.debug)
	}
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	h.mu.RLock()
	apiKey, model := h.apiKey, h.model
	h.mu.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "tool-stability",
		"version":    "1.0.0",
		"configured": apiKey != "",
		"model":      model,
	})
}

type generateRequest struct {
	Prompt         string  `json:"prompt" binding:"required"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	OutputFormat   string  `json:"output_format,omitempty"`
	StylePreset    string  `json:"style_preset,omitempty"`
	CfgScale       float64 `json:"cfg_scale,omitempty"`
	Seed           int64   `json:"seed,omitempty"`
}

// Generate creates an image from a text prompt via Stability AI.
func (h *Handler) Generate(c *gin.Context) {
	h.mu.RLock()
	apiKey, model, client := h.apiKey, h.model, h.client
	h.mu.RUnlock()

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set STABILITY_API_KEY."})
		return
	}

	var req generateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: prompt is required"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(req.Prompt, 100)))

	start := time.Now()

	result, err := client.Generate(stability.GenerateRequest{
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		AspectRatio:    req.AspectRatio,
		OutputFormat:   req.OutputFormat,
		StylePreset:    req.StylePreset,
		CfgScale:       req.CfgScale,
		Seed:           req.Seed,
	})
	if err != nil {
		log.Printf("Stability generate error: %v", err)
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
				Provider:   "stability",
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
			Provider:   "stability",
			Model:      model,
			RecordType: "request",
			Status:     "completed",
			Prompt:     truncateStr(req.Prompt, 200),
			DurationMs: elapsed.Milliseconds(),
		})
	}

	h.emitEvent("generate_complete", fmt.Sprintf("model=%s duration=%dms seed=%s", model, elapsed.Milliseconds(), result.Seed))

	c.JSON(http.StatusOK, gin.H{
		"status":     "completed",
		"image_data": result.ImageData,
		"mime_type":  result.MimeType,
		"seed":       result.Seed,
		"model":      model,
		"prompt":     req.Prompt,
	})
}

type chatRequest struct {
	Message      string `json:"message"`
	Model        string `json:"model,omitempty"`
	Conversation []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"conversation"`
}

// Chat wraps Generate and returns a chat-format response with the image
// embedded as an attachment.
func (h *Handler) Chat(c *gin.Context) {
	h.mu.RLock()
	apiKey, model, client := h.apiKey, h.model, h.client
	h.mu.RUnlock()

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set STABILITY_API_KEY."})
		return
	}

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Model != "" {
		model = req.Model
	}

	// Extract prompt: prefer last user message from conversation, fall back to message field.
	prompt := req.Message
	for i := len(req.Conversation) - 1; i >= 0; i-- {
		if req.Conversation[i].Role == "user" && req.Conversation[i].Content != "" {
			prompt = req.Conversation[i].Content
			break
		}
	}
	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")
	h.emitEvent("chat_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(prompt, 100)))

	start := time.Now()

	result, err := client.Generate(stability.GenerateRequest{Prompt: prompt})
	if err != nil {
		log.Printf("Stability chat/generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("chat: %v", err))
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID: userID, Provider: "stability", Model: model,
				RecordType: "request", Status: "failed",
				Prompt: truncateStr(prompt, 200), DurationMs: time.Since(start).Milliseconds(),
			})
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "Image generation failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:      model,
		Prompt:     truncateStr(prompt, 200),
		Status:     "completed",
		DurationMs: elapsed.Milliseconds(),
	})

	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID: userID, Provider: "stability", Model: model,
			RecordType: "request", Status: "completed",
			Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
		})
	}

	h.emitEvent("chat_response", fmt.Sprintf("model=%s duration=%dms", model, elapsed.Milliseconds()))

	c.JSON(http.StatusOK, gin.H{
		"response": "Here is the generated image.",
		"model":    model,
		"attachments": []gin.H{
			{
				"mime_type":  result.MimeType,
				"image_data": result.ImageData,
			},
		},
	})
}

// Models returns the static model list and current selection.
func (h *Handler) Models(c *gin.Context) {
	h.mu.RLock()
	model := h.model
	h.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"models":  []string{"sd3.5-large", "sd3.5-large-turbo", "sd3.5-medium", "sd3.5-flash"},
		"current": model,
	})
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

// ToolDefs returns the raw tool definitions for this plugin.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "generate_image",
			"description": "Generate an image from a text prompt using Stability AI (Stable Diffusion 3.5)",
			"endpoint":    "/generate",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prompt":          gin.H{"type": "string", "description": "Text prompt describing the image to generate (1-10000 chars)"},
					"negative_prompt": gin.H{"type": "string", "description": "What to exclude from the image (not supported on turbo/flash models)"},
					"aspect_ratio":    gin.H{"type": "string", "description": "Aspect ratio for the image", "enum": []string{"1:1", "16:9", "9:16", "21:9", "9:21", "2:3", "3:2", "4:5", "5:4"}},
					"output_format":   gin.H{"type": "string", "description": "Output image format", "enum": []string{"png", "jpeg", "webp"}},
					"style_preset":    gin.H{"type": "string", "description": "Guide the image towards a particular style", "enum": []string{"3d-model", "analog-film", "anime", "cinematic", "comic-book", "digital-art", "enhance", "fantasy-art", "isometric", "line-art", "low-poly", "modeling-compound", "neon-punk", "origami", "photographic", "pixel-art", "tile-texture"}},
					"cfg_scale":       gin.H{"type": "number", "description": "How strictly to follow the prompt (1-10, default varies by model)"},
					"seed":            gin.H{"type": "integer", "description": "Seed for reproducible generation (0 for random)"},
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
	c.JSON(http.StatusOK, gin.H{
		"system_prompt": buildSystemPrompt(h.model),
	})
}

func buildSystemPrompt(model string) string {
	return fmt.Sprintf(`You are an image generation tool powered by Stability AI (Stable Diffusion 3.5).

CAPABILITIES:
- Generate images from text prompts
- Current model: %s
- Supported aspect ratios: 1:1, 16:9, 9:16, 21:9, 9:21, 2:3, 3:2, 4:5, 5:4
- Supported output formats: png, jpeg, webp
- Style presets: 3d-model, analog-film, anime, cinematic, comic-book, digital-art, enhance, fantasy-art, isometric, line-art, low-poly, modeling-compound, neon-punk, origami, photographic, pixel-art, tile-texture

PARAMETERS:
- prompt (required): Text description of the image to generate
- negative_prompt (optional): What to exclude (not supported on turbo/flash models)
- aspect_ratio (optional): Image aspect ratio (default: 1:1)
- output_format (optional): Output format (default: png)
- style_preset (optional): Guide the image towards a particular style
- cfg_scale (optional): Prompt adherence strength 1-10 (default varies by model)
- seed (optional): Seed for reproducible results (0 for random)

OUTPUT:
- Returns base64-encoded image data with MIME type and seed value`, model)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
