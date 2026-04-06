package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/anthropic"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/usage"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
type Handler struct {
	mu            sync.RWMutex
	backend       string // "cli" or "api_key"
	apiKey        string
	model         string
	toolLoopLimit int
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	claudeCLI     *claudecli.Client
	usage         *usage.Tracker
	mcpConfig     string // path to MCP config file, if available
	mcpPluginID   string // plugin ID of infra-mcp-server for proxy routing
	workspaceDir  string // base directory for workspace mounts
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	Backend             string
	APIKey              string
	Model               string
	Debug               bool
	DataPath            string
	WorkspaceDir        string
	DefaultSystemPrompt string
}

// NewHandler creates a new Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		backend:       cfg.Backend,
		apiKey:        cfg.APIKey,
		model:         cfg.Model,
		toolLoopLimit: 20,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultSystemPrompt,
		usage:         usage.NewTracker(cfg.DataPath),
		workspaceDir:  cfg.WorkspaceDir,
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

// CyclePool cycles the CLI process pool so new processes pick up fresh config.
func (h *Handler) CyclePool() {
	if h.claudeCLI != nil {
		h.claudeCLI.CyclePool()
	}
}

// SetMCPConfig sets the path to the MCP config file.
func (h *Handler) SetMCPConfig(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mcpConfig = path
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["CLAUDE_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
	}
	if v, ok := config["ANTHROPIC_API_KEY"]; ok {
		h.apiKey = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
	if v, ok := config["CLAUDE_SKIP_PERMISSIONS"]; ok {
		skip := v == "true"
		if h.claudeCLI != nil {
			h.claudeCLI.SetSkipPermissions(skip)
			log.Printf("[config] skip-permissions: %v", skip)
		}
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
	backend := h.backend
	apiKey := h.apiKey
	h.mu.RUnlock()
	configured := false
	switch backend {
	case "cli":
		configured = h.claudeCLI != nil && h.claudeCLI.IsAvailable()
	case "api_key":
		configured = apiKey != ""
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-claude",
		"version":    "1.0.0",
		"configured": configured,
		"backend":    backend,
	})
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
// lastUserMessage returns the content of the last user message in the list.
func lastUserMessage(messages []anthropic.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// buildPromptWithSystem flattens messages into a single prompt string,
// prepending the system prompt if provided. Used as fallback when there
// is no extractable user message.
func buildPromptWithSystem(messages []anthropic.Message, systemPrompt string) string {
	if systemPrompt != "" {
		filtered := make([]anthropic.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]anthropic.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}
	return buildPrompt(messages)
}

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

// DiscoveredTools returns the tools this agent has discovered from tool:* plugins.
func (h *Handler) DiscoveredTools(c *gin.Context) {
	tools := discoverTools(h.sdk)

	type toolEntry struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Endpoint    string          `json:"endpoint"`
		Parameters  json.RawMessage `json:"parameters"`
		PluginID    string          `json:"plugin_id"`
	}

	entries := make([]toolEntry, len(tools))
	for i, t := range tools {
		entries[i] = toolEntry{
			Name:        t.PrefixedName,
			Description: t.Description,
			Endpoint:    t.Endpoint,
			Parameters:  t.Parameters,
			PluginID:    t.PluginID,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"tools": entries,
	})
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
