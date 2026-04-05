package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimi"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimicli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/usage"
)

type Handler struct {
	mu            sync.RWMutex
	apiKey        string
	model         string
	toolLoopLimit int
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	client        *kimi.Client
	kimiCLI       *kimicli.Client
	mcpConfigFile string
	usage         *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool, defaultPrompt string) *Handler {
	return &Handler{
		apiKey:        apiKey,
		model:         model,
		toolLoopLimit: 20,
		debug:         debug,
		defaultPrompt: defaultPrompt,
		client:        kimi.NewClient(apiKey, debug),
		usage:         usage.NewTracker(dataPath),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) SetKimiCLI(cli *kimicli.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kimiCLI = cli
}

func (h *Handler) SetMCPConfigFile(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mcpConfigFile = path
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["KIMI_API_KEY"]; ok {
		if v != h.apiKey {
			log.Printf("[config] updating api key")
			h.apiKey = v
			h.client = kimi.NewClient(v, h.debug)
		}
	}
	if v, ok := config["KIMI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-kimi",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
		"model":      h.model,
	})
}

// SystemPrompt returns the system prompt this agent would use, plus
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
		log.Printf("ListModels error: %v", err)
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
	case "KIMI_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured."})
			return
		}
		models, err := h.client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
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
