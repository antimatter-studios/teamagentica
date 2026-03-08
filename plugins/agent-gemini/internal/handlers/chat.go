package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
)

type Handler struct {
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *gemini.Client
	usage  *usage.Tracker
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	APIKey   string
	Model    string
	Debug    bool
	DataPath string
}

// NewHandler creates a new Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		debug:  cfg.Debug,
		client: gemini.NewClient(cfg.APIKey, cfg.Debug),
		usage:  usage.NewTracker(cfg.DataPath),
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
		"plugin":     "agent-gemini",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
		"model":      h.model,
	})
}

type chatRequest struct {
	Message      string           `json:"message"`
	Model        string           `json:"model,omitempty"`
	ImageURLs    []string         `json:"image_urls,omitempty"`
	Conversation []gemini.Message `json:"conversation"`
}

func (h *Handler) Chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []gemini.Message{
			{Role: "user", Content: req.Message},
		}
	}

	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set GEMINI_API_KEY."})
		return
	}

	// Use per-request model override if provided.
	model := h.model
	if req.Model != "" {
		model = req.Model
	}

	lastMsg := ""
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1].Content
	}
	if h.debug {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d content=%s", model, len(messages), truncateStr(lastMsg, 200)))
	} else {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d", model, len(messages)))
	}

	start := time.Now()

	resp, err := h.client.ChatCompletion(model, messages, req.ImageURLs...)
	if err != nil {
		log.Printf("Gemini error: %v", err)
		h.emitEvent("error", fmt.Sprintf("gemini: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "Gemini request failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:        model,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  resp.Usage.TotalTokens,
		CachedTokens: resp.Usage.CachedTokens,
		DurationMs:   elapsed.Milliseconds(),
	})

	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:       userID,
			Provider:     "gemini",
			Model:        model,
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
			CachedTokens: resp.Usage.CachedTokens,
			DurationMs:   elapsed.Milliseconds(),
		})
	}

	if h.debug {
		h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
			model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds(), truncateStr(resp.Content, 200)))
	} else {
		h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
			model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds(), len(resp.Content)))
	}

	c.JSON(http.StatusOK, gin.H{
		"response": resp.Content,
		"model":    model,
		"backend":  "gemini",
		"usage": gin.H{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
		},
	})
}

// Models returns the list of available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusOK, gin.H{
			"models":  gemini.DefaultModels(),
			"current": h.model,
		})
		return
	}

	models, _, err := h.client.ListModels()
	if err != nil {
		log.Printf("ListModels error: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"models":  gemini.DefaultModels(),
			"current": h.model,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"models":  models,
		"current": h.model,
	})
}

func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "GEMINI_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured."})
			return
		}
		models, fallback, err := h.client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
			c.JSON(http.StatusOK, gin.H{"options": models, "fallback": fallback, "error": "Failed to fetch models: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": models, "fallback": fallback})
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
