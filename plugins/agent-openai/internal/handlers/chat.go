package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/usage"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
// Mutable config fields are protected by mu and updated via ApplyConfig.
type Handler struct {
	mu            sync.RWMutex
	backend       string // "subscription" or "api_key"
	apiKey        string
	model         string
	endpoint      string
	toolLoopLimit int
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	codexCLI      *codexcli.Client
	usage         *usage.Tracker
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	Backend             string
	APIKey              string
	Model               string
	Endpoint            string
	ToolLoopLimit       int
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
		endpoint:      cfg.Endpoint,
		toolLoopLimit: cfg.ToolLoopLimit,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultSystemPrompt,
		usage:         usage.NewTracker(cfg.DataPath),
	}
}

// SetSDK attaches the plugin SDK client for event reporting.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// emitEvent sends a debug event to the kernel console.
func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

// SetCodexCLI attaches the Codex CLI client to the handler.
func (h *Handler) SetCodexCLI(client *codexcli.Client) {
	h.codexCLI = client
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["OPENAI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
	}
	if v, ok := config["OPENAI_API_KEY"]; ok {
		h.apiKey = v
	}
	if v, ok := config["OPENAI_API_ENDPOINT"]; ok && v != "" {
		log.Printf("[config] updating endpoint: %s → %s", h.endpoint, v)
		h.endpoint = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
	if v, ok := config["TOOL_LOOP_LIMIT"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			log.Printf("[config] updating tool_loop_limit: %d → %d", h.toolLoopLimit, n)
			h.toolLoopLimit = n
		}
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
	case "subscription":
		configured = h.codexCLI != nil && h.codexCLI.IsAuthenticated()
	case "api_key":
		configured = apiKey != ""
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-openai",
		"version":    "1.0.0",
		"configured": configured,
		"backend":    backend,
	})
}

// emitUsage sends a usage report via the SDK for guaranteed delivery to infra-cost-tracking.
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

// SystemPrompt returns the system prompt this agent would use, plus
// rendered previews for every persona/alias that routes through this plugin.
func (h *Handler) SystemPrompt(c *gin.Context) {
	resp := gin.H{"default_prompt": h.defaultPrompt}

	if h.sdk != nil {
		if previews, err := h.sdk.SystemPromptPreview(h.sdk.PluginID(), h.defaultPrompt); err != nil {
			log.Printf("system-prompt preview: %v", err)
		} else if len(previews) > 0 {
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

// truncateStr shortens a string for debug logging.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Models endpoint --------------------------------------------------------

// Models returns available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	h.mu.RLock()
	backend := h.backend
	apiKey := h.apiKey
	endpoint := h.endpoint
	model := h.model
	h.mu.RUnlock()

	if backend == "subscription" && h.codexCLI != nil {
		models, err := h.codexCLI.ListModels()
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"models": models, "current": model})
			return
		}
		log.Printf("ListModels error: %v", err)
	} else if apiKey != "" {
		models, err := openai.ListModels(apiKey, endpoint)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"models": models, "current": model})
			return
		}
		log.Printf("ListModels error: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"models":  []string{},
		"current": model,
		"error":   "Unable to fetch models",
	})
}

// --- Config options endpoints -----------------------------------------------

// ConfigOptions returns dynamic select options for a config field.
// GET /config/options/:field → {"options": [...], "error": "..."}
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	h.mu.RLock()
	backend := h.backend
	apiKey := h.apiKey
	endpoint := h.endpoint
	h.mu.RUnlock()

	switch field {
	case "OPENAI_MODEL":
		if backend == "subscription" && h.codexCLI != nil {
			models, err := h.codexCLI.ListModels()
			if err != nil {
				log.Printf("ListModels error: %v", err)
				c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Failed to fetch models: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"options": models})
			return
		} else if apiKey != "" {
			models, err := openai.ListModels(apiKey, endpoint)
			if err != nil {
				log.Printf("ListModels error: %v", err)
				c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Failed to fetch models: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"options": models})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No credentials available"})

	default:
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Unknown field"})
	}
}

// --- Codex auth endpoints --------------------------------------------------

// AuthStatus returns the current Codex CLI authentication state.
func (h *Handler) AuthStatus(c *gin.Context) {
	if h.codexCLI == nil {
		c.JSON(http.StatusOK, gin.H{
			"codex_enabled": false,
			"authenticated": false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"codex_enabled": true,
		"authenticated": h.codexCLI.IsAuthenticated(),
	})
}

// AuthDeviceCode starts the Codex CLI device-code login flow and returns
// the URL and one-time code the user needs to complete auth in a browser.
func (h *Handler) AuthDeviceCode(c *gin.Context) {
	if h.codexCLI == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Codex is not enabled"})
		return
	}
	result, err := h.codexCLI.StartLogin()
	if err != nil {
		log.Printf("StartLogin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"url":  result.URL,
		"code": result.Code,
	})
}

// AuthPoll checks whether the background device-code login has completed.
func (h *Handler) AuthPoll(c *gin.Context) {
	if h.codexCLI == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Codex is not enabled"})
		return
	}
	done, err := h.codexCLI.PollLogin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"authenticated": done,
	})
}

// AuthLogout clears Codex CLI stored tokens.
func (h *Handler) AuthLogout(c *gin.Context) {
	if h.codexCLI == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Codex is not enabled"})
		return
	}
	if err := h.codexCLI.Logout(); err != nil {
		log.Printf("Logout error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

// --- Usage endpoint --------------------------------------------------------

// Usage returns accumulated usage stats and current rate limit info.
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
