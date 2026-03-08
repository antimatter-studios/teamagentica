package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/anthropic"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/usage"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
type Handler struct {
	backend      string // "cli" or "api_key"
	apiKey       string
	model        string
	debug        bool
	sdk          *pluginsdk.Client
	claudeCLI    *claudecli.Client
	usage        *usage.Tracker
	mcpConfig    string // path to MCP config file, if available
	workspaceDir string // base directory for workspace mounts
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	Backend      string
	APIKey       string
	Model        string
	Debug        bool
	DataPath     string
	WorkspaceDir string
}

// NewHandler creates a new Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		backend:      cfg.Backend,
		apiKey:       cfg.APIKey,
		model:        cfg.Model,
		debug:        cfg.Debug,
		usage:        usage.NewTracker(cfg.DataPath),
		workspaceDir: cfg.WorkspaceDir,
	}
}

// SetSDK attaches the plugin SDK client for event reporting.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// SetClaudeCLI attaches the Claude CLI client to the handler.
func (h *Handler) SetClaudeCLI(client *claudecli.Client) {
	h.claudeCLI = client
}

// SetMCPConfig sets the path to the MCP config file.
func (h *Handler) SetMCPConfig(path string) {
	h.mcpConfig = path
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	configured := false
	switch h.backend {
	case "cli":
		configured = h.claudeCLI != nil && h.claudeCLI.IsAvailable()
	case "api_key":
		configured = h.apiKey != ""
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-claude",
		"version":    "1.0.0",
		"configured": configured,
		"backend":    h.backend,
	})
}

// chatRequest is the body for POST /chat.
type chatRequest struct {
	Message      string             `json:"message"`
	Model        string             `json:"model,omitempty"`
	Conversation []anthropic.Message `json:"conversation"`
	MaxTurns     int                `json:"max_turns,omitempty"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	WorkspaceID  string             `json:"workspace_id,omitempty"`  // Routes to a specific work disk.
	SessionID    string             `json:"session_id,omitempty"`    // Resumes an existing Claude session.
}

// Chat handles a chat completion request.
func (h *Handler) Chat(c *gin.Context) {
	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

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
		messages = []anthropic.Message{
			{Role: "user", Content: req.Message},
		}
	}

	model := h.model
	if req.Model != "" {
		model = req.Model
	}

	lastMsg := ""
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1].Content
	}
	if h.debug {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d workspace=%s session=%s content=%s",
			model, len(messages), req.WorkspaceID, req.SessionID, truncateStr(lastMsg, 200)))
	} else {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d workspace=%s session=%s",
			model, len(messages), req.WorkspaceID, req.SessionID))
	}

	start := time.Now()

	switch h.backend {
	case "cli":
		if h.claudeCLI == nil {
			h.emitEvent("error", "CLI backend not initialised")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "CLI backend is configured but Claude CLI was not initialised."})
			return
		}

		// Build a single prompt from the conversation.
		prompt := buildPrompt(messages)

		// Build workspace/session options.
		var opts *claudecli.ChatOptions
		if req.WorkspaceID != "" || req.SessionID != "" {
			opts = &claudecli.ChatOptions{
				SessionID: req.SessionID,
			}
			if req.WorkspaceID != "" {
				wsPath := h.workspaceDir + "/" + req.WorkspaceID
				if !isValidWorkspaceID(req.WorkspaceID) {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace_id: must be alphanumeric, hyphens, or underscores"})
					return
				}
				opts.WorkspaceDir = wsPath
			}
		}

		resp, err := h.claudeCLI.ChatCompletion(model, prompt, req.SystemPrompt, req.MaxTurns, nil, h.mcpConfig, opts)
		if err != nil {
			log.Printf("Claude CLI error: %v", err)
			h.emitEvent("error", fmt.Sprintf("cli: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Claude request failed: " + err.Error()})
			return
		}

		elapsed := time.Since(start)

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        resp.Model,
			InputTokens:  resp.InputTokens,
			OutputTokens: resp.OutputTokens,
			TotalTokens:  resp.InputTokens + resp.OutputTokens,
			CachedTokens: resp.CachedTokens,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "cli",
		})
		h.emitUsage("anthropic", resp.Model, resp.InputTokens, resp.OutputTokens, resp.InputTokens+resp.OutputTokens, resp.CachedTokens, elapsed.Milliseconds(), userID)

		if h.debug {
			h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d turns=%d cost=$%.4f time=%dms response=%s",
				resp.Model, resp.InputTokens, resp.OutputTokens, resp.NumTurns, resp.CostUSD, elapsed.Milliseconds(), truncateStr(resp.Response, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d turns=%d cost=$%.4f time=%dms len=%d",
				resp.Model, resp.InputTokens, resp.OutputTokens, resp.NumTurns, resp.CostUSD, elapsed.Milliseconds(), len(resp.Response)))
		}

		c.JSON(http.StatusOK, gin.H{
			"response": resp.Response,
			"model":    resp.Model,
			"backend":  "cli",
			"usage": gin.H{
				"prompt_tokens":     resp.InputTokens,
				"completion_tokens": resp.OutputTokens,
				"cached_tokens":     resp.CachedTokens,
			},
			"cost_usd":   resp.CostUSD,
			"num_turns":  resp.NumTurns,
			"session_id": resp.SessionID,
		})

	case "api_key":
		if h.apiKey == "" {
			h.emitEvent("error", "api_key backend has no ANTHROPIC_API_KEY")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api_key backend is configured but ANTHROPIC_API_KEY is not set."})
			return
		}

		resp, err := anthropic.ChatCompletion(h.apiKey, model, messages, 8192)
		if err != nil {
			log.Printf("Anthropic error: %v", err)
			h.emitEvent("error", fmt.Sprintf("anthropic: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Anthropic request failed: " + err.Error()})
			return
		}

		elapsed := time.Since(start)
		totalTokens := resp.Usage.InputTokens + resp.Usage.OutputTokens

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  totalTokens,
			CachedTokens: resp.Usage.CacheRead,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "api_key",
		})
		h.emitUsage("anthropic", model, resp.Usage.InputTokens, resp.Usage.OutputTokens, totalTokens, resp.Usage.CacheRead, elapsed.Milliseconds(), userID)

		responseText := anthropic.GetResponseText(resp)
		if h.debug {
			h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms response=%s",
				model, resp.Usage.InputTokens, resp.Usage.OutputTokens, elapsed.Milliseconds(), truncateStr(responseText, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms len=%d",
				model, resp.Usage.InputTokens, resp.Usage.OutputTokens, elapsed.Milliseconds(), len(responseText)))
		}

		c.JSON(http.StatusOK, gin.H{
			"response": responseText,
			"model":    model,
			"backend":  "api_key",
			"usage": gin.H{
				"prompt_tokens":     resp.Usage.InputTokens,
				"completion_tokens": resp.Usage.OutputTokens,
				"cached_tokens":     resp.Usage.CacheRead,
			},
		})

	default:
		h.emitEvent("error", fmt.Sprintf("unknown backend: %s", h.backend))
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unknown backend %q. Set CLAUDE_BACKEND to 'cli' or 'api_key'.", h.backend)})
	}
}

func (h *Handler) emitUsage(provider, model string, inputTokens, outputTokens, totalTokens, cachedTokens int, durationMs int64, userID string) {
	if h.sdk == nil {
		return
	}
	h.sdk.ReportUsage(pluginsdk.UsageReport{
		UserID:       userID,
		Provider:     provider,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		CachedTokens: cachedTokens,
		DurationMs:   durationMs,
	})
}

// buildPrompt concatenates conversation messages into a single prompt string.
func buildPrompt(messages []anthropic.Message) string {
	if len(messages) == 1 {
		return messages[0].Content
	}

	var sb strings.Builder
	for i, msg := range messages {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch msg.Role {
		case "user":
			sb.WriteString("User: ")
		case "assistant":
			sb.WriteString("Assistant: ")
		case "system":
			sb.WriteString("System: ")
		}
		sb.WriteString(msg.Content)
	}
	return sb.String()
}

// Models returns available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	models := []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "current": h.model})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "CLAUDE_MODEL":
		models := []string{
			"claude-opus-4-6",
			"claude-sonnet-4-6",
			"claude-haiku-4-5-20251001",
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

// UsageRecords returns raw request records.
func (h *Handler) UsageRecords(c *gin.Context) {
	since := c.Query("since")
	records := h.usage.Records(since)
	if records == nil {
		records = []usage.RequestRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}


// --- Helpers ----------------------------------------------------------------

// isValidWorkspaceID checks that a workspace ID is safe for use as a directory name.
func isValidWorkspaceID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
