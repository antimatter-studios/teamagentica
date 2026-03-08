package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/nanobanana"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/usage"
)

type Handler struct {
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

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "tool-nanobanana",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
		"default_model": h.model,
	})
}

type generateRequest struct {
	Prompt      string `json:"prompt" binding:"required"`
	Model       string `json:"model,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

// Generate creates an image from a text prompt. Unlike video tools, this is
// synchronous — the image data is returned directly in the response.
func (h *Handler) Generate(c *gin.Context) {
	if h.apiKey == "" {
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
		model = h.model
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("model=%s prompt=%s", model, truncateStr(req.Prompt, 100)))

	start := time.Now()

	result, err := h.client.Generate(nanobanana.GenerateRequest{
		Prompt:      req.Prompt,
		Model:       model,
		AspectRatio: req.AspectRatio,
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

	h.emitEvent("generate_complete", fmt.Sprintf("model=%s duration=%dms mime=%s", model, elapsed.Milliseconds(), result.MimeType))

	c.JSON(http.StatusOK, gin.H{
		"status":     "completed",
		"image_data": result.ImageData,
		"mime_type":  result.MimeType,
		"text":       result.Text,
		"model":      model,
		"prompt":     req.Prompt,
	})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "NANOBANANA_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured"})
			return
		}
		models, err := h.client.ListModels()
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

// chatRequest matches the shape used by agent-openai and the web UI.
type chatRequest struct {
	Message      string `json:"message"`
	Model        string `json:"model,omitempty"`
	Conversation []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"conversation"`
}

// Chat wraps Generate and returns a chat-format response with the image
// embedded as a markdown data URL.
func (h *Handler) Chat(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set GEMINI_API_KEY."})
		return
	}

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	model := req.Model
	if model == "" {
		model = h.model
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

	result, err := h.client.Generate(nanobanana.GenerateRequest{Prompt: prompt, Model: model})
	if err != nil {
		log.Printf("Nano Banana chat/generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("chat: %v", err))
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID: userID, Provider: "nanobanana", Model: model,
				RecordType: "request", Status: "failed",
				Prompt: truncateStr(prompt, 200), DurationMs: time.Since(start).Milliseconds(),
			})
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "Image generation failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID: userID, Provider: "nanobanana", Model: model,
			RecordType: "request", Status: "completed",
			Prompt: truncateStr(prompt, 200), DurationMs: elapsed.Milliseconds(),
		})
	}

	responseText := "Here is the generated image."
	if result.Text != "" {
		responseText = result.Text
	}

	h.emitEvent("chat_response", fmt.Sprintf("model=%s duration=%dms", model, elapsed.Milliseconds()))

	c.JSON(http.StatusOK, gin.H{
		"response": responseText,
		"model":    model,
		"attachments": []gin.H{
			{
				"mime_type":  result.MimeType,
				"image_data": result.ImageData,
			},
		},
	})
}

// Tools returns the available tool schemas for this plugin.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tools": []gin.H{
			{
				"name":        "generate_image",
				"description": "Generate an image from a text prompt using Nano Banana (Gemini image model)",
				"endpoint":    "/generate",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"prompt":       gin.H{"type": "string", "description": "Text prompt describing the image to generate"},
						"aspect_ratio": gin.H{"type": "string", "description": "Aspect ratio (1:1, 16:9, 9:16)", "enum": []string{"1:1", "16:9", "9:16"}},
					},
					"required": []string{"prompt"},
				},
			},
		},
	})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
