package handlers

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/openrouter"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/usage"
)

// Handler holds plugin-specific configuration and exposes HTTP handlers for
// routes that are NOT covered by agentkit (usage, models, config options, etc.).
//
// The /chat, /health, and /mcp routes are now handled by agentkit.RegisterAgentChat.
type Handler struct {
	mu     sync.RWMutex
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *openrouter.Client
	usage  *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: openrouter.NewClient(apiKey, debug),
		usage:  usage.NewTracker(dataPath),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// Tracker returns the usage tracker for sharing with the adapter.
func (h *Handler) Tracker() *usage.Tracker {
	return h.usage
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["OPENROUTER_API_KEY"]; ok {
		if v != h.apiKey {
			h.apiKey = v
			h.client = openrouter.NewClient(v, h.debug)
		}
	}
	if v, ok := config["OPENROUTER_MODEL"]; ok && v != "" {
		h.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
}

// Models returns the list of available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusOK, gin.H{
			"models":  []string{},
			"current": h.model,
			"error":   "No API key configured.",
		})
		return
	}

	models, err := h.client.ListModels()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"models":  []string{},
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
	case "OPENROUTER_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured."})
			return
		}
		models, err := h.client.ListModels()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Failed to fetch models: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": models})
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
