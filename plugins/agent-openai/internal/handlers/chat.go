package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"roboslop/plugins/agent-openai/internal/config"
	"roboslop/plugins/agent-openai/internal/openai"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
// Config is immutable after construction — it comes from env vars injected
// by the kernel. There are no runtime config endpoints.
type Handler struct {
	apiKey   string
	model    string
	endpoint string
}

// NewHandler creates a new Handler from env-var-based config.
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		apiKey:   cfg.OpenAIAPIKey,
		model:    cfg.OpenAIModel,
		endpoint: cfg.OpenAIEndpoint,
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	configured := h.apiKey != ""
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-openai",
		"version":    "1.0.0",
		"configured": configured,
	})
}

// chatRequest is the body for POST /chat.
type chatRequest struct {
	Message      string           `json:"message"`
	Conversation []openai.Message `json:"conversation"`
}

// Chat handles a chat completion request.
func (h *Handler) Chat(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "OpenAI API key not configured. Set OPENAI_API_KEY via plugin config.",
		})
		return
	}

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []openai.Message{
			{Role: "user", Content: req.Message},
		}
	}

	resp, err := openai.ChatCompletion(h.apiKey, h.endpoint, h.model, messages)
	if err != nil {
		log.Printf("OpenAI error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "OpenAI request failed: " + err.Error()})
		return
	}

	responseText := ""
	if len(resp.Choices) > 0 {
		responseText = resp.Choices[0].Message.Content
	}

	c.JSON(http.StatusOK, gin.H{
		"response": responseText,
		"model":    h.model,
		"usage": gin.H{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
		},
	})
}
