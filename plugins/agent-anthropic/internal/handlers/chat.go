package handlers

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/usage"
)

// Handler holds plugin-specific configuration and exposes HTTP handlers for
// routes that are NOT covered by agentkit (auth, MCP proxy, usage, models, etc.).
//
// The /chat, /health, and /mcp routes are now handled by agentkit.RegisterAgentChat.
type Handler struct {
	mu            sync.RWMutex
	backend       string // "cli" or "api_key"
	apiKey        string
	model         string
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	claudeCLI     *claudecli.Client
	usage         *usage.Tracker
	mcpPluginID   string // plugin ID of infra-mcp-server for proxy routing
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	Backend             string
	APIKey              string
	Model               string
	Debug               bool
	DataPath            string
	DefaultSystemPrompt string
}

// NewHandler creates a new Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		backend:       cfg.Backend,
		apiKey:        cfg.APIKey,
		model:         cfg.Model,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultSystemPrompt,
		usage:         usage.NewTracker(cfg.DataPath),
	}
}

// SetSDK attaches the plugin SDK client for event reporting.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// SetClaudeCLI attaches the Claude CLI client to the handler (for auth routes).
func (h *Handler) SetClaudeCLI(client *claudecli.Client) {
	h.claudeCLI = client
}

// Tracker returns the usage tracker for sharing with the adapter.
func (h *Handler) Tracker() *usage.Tracker {
	return h.usage
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["CLAUDE_MODEL"]; ok && v != "" {
		h.model = v
	}
	if v, ok := config["ANTHROPIC_API_KEY"]; ok {
		h.apiKey = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
}

// SystemPrompt returns the default system prompt this agent would use, plus
// rendered previews for every persona/alias that routes through this plugin.
func (h *Handler) SystemPrompt(c *gin.Context) {
	resp := gin.H{"default_prompt": h.defaultPrompt}

	if h.sdk != nil {
		if previews, err := h.sdk.SystemPromptPreview(h.sdk.PluginID(), h.defaultPrompt); err == nil && len(previews) > 0 {
			resp["aliases"] = previews
		}
	}

	c.JSON(http.StatusOK, resp)
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
