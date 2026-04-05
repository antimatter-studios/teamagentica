package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/inception"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/usage"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
type Handler struct {
	mu            sync.RWMutex
	apiKey        string
	model         string
	endpoint      string
	diffusing     bool
	instant       bool
	toolLoopLimit int
	debug         bool
	sdk           *pluginsdk.Client
	usage         *usage.Tracker
	defaultPrompt string
}

// NewHandler creates a new Handler with the given configuration values.
func NewHandler(apiKey, model, endpoint, dataPath string, debug, diffusing, instant bool, toolLoopLimit int, defaultPrompt string) *Handler {
	return &Handler{
		apiKey:        apiKey,
		model:         model,
		endpoint:      endpoint,
		diffusing:     diffusing,
		instant:       instant,
		toolLoopLimit: toolLoopLimit,
		debug:         debug,
		usage:         usage.NewTracker(dataPath),
		defaultPrompt: defaultPrompt,
	}
}

// SetSDK attaches the plugin SDK client for event reporting.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["INCEPTION_API_KEY"]; ok {
		h.apiKey = v
	}
	if v, ok := config["INCEPTION_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
	}
	if v, ok := config["INCEPTION_API_ENDPOINT"]; ok && v != "" {
		log.Printf("[config] updating endpoint: %s → %s", h.endpoint, v)
		h.endpoint = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		h.debug = v == "true"
	}
	if v, ok := config["INCEPTION_DIFFUSING"]; ok {
		h.diffusing = v == "true"
	}
	if v, ok := config["INCEPTION_INSTANT"]; ok {
		h.instant = v == "true"
	}
	if v, ok := config["TOOL_LOOP_LIMIT"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			log.Printf("[config] updating tool_loop_limit: %d → %d", h.toolLoopLimit, n)
			h.toolLoopLimit = n
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
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-inception",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
	})
}

// ApplyEditRequest is the body for POST /apply-edit.
type ApplyEditRequest struct {
	OriginalCode  string `json:"original_code"`
	UpdateSnippet string `json:"update_snippet"`
}

// ApplyEdit handles an apply-edit request using the mercury-edit model.
func (h *Handler) ApplyEdit(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "INCEPTION_API_KEY is not set."})
		return
	}

	var req ApplyEditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.OriginalCode == "" || req.UpdateSnippet == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "original_code and update_snippet required"})
		return
	}

	h.emitEvent("apply_edit_request", fmt.Sprintf("original_len=%d snippet_len=%d", len(req.OriginalCode), len(req.UpdateSnippet)))

	start := time.Now()
	resp, err := inception.ApplyEdit(h.apiKey, h.endpoint, req.OriginalCode, req.UpdateSnippet)
	if err != nil {
		log.Printf("Apply-edit error: %v", err)
		h.emitEvent("error", fmt.Sprintf("apply-edit: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "Apply-edit request failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)
	responseText := ""
	if len(resp.Choices) > 0 {
		responseText = resp.Choices[0].Message.Content
	}

	h.emitEvent("apply_edit_response", fmt.Sprintf("tokens=%d+%d time=%dms", resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds()))

	c.JSON(http.StatusOK, gin.H{
		"response": responseText,
		"model":    "mercury-edit",
		"usage": gin.H{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
		},
	})
}

// NextEditRequest is the body for POST /next-edit.
type NextEditRequest struct {
	RecentSnippets     string `json:"recent_snippets"`
	CurrentFileContent string `json:"current_file_content"`
	EditDiffHistory    string `json:"edit_diff_history"`
}

// NextEdit handles a next-edit request using the mercury-edit model.
func (h *Handler) NextEdit(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "INCEPTION_API_KEY is not set."})
		return
	}

	var req NextEditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.CurrentFileContent == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current_file_content required"})
		return
	}

	h.emitEvent("next_edit_request", fmt.Sprintf("file_len=%d diff_len=%d", len(req.CurrentFileContent), len(req.EditDiffHistory)))

	start := time.Now()
	resp, err := inception.NextEdit(h.apiKey, h.endpoint, req.RecentSnippets, req.CurrentFileContent, req.EditDiffHistory)
	if err != nil {
		log.Printf("Next-edit error: %v", err)
		h.emitEvent("error", fmt.Sprintf("next-edit: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "Next-edit request failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)
	responseText := ""
	if len(resp.Choices) > 0 {
		responseText = resp.Choices[0].Message.Content
	}

	h.emitEvent("next_edit_response", fmt.Sprintf("tokens=%d+%d time=%dms", resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds()))

	c.JSON(http.StatusOK, gin.H{
		"response": responseText,
		"model":    "mercury-edit",
		"usage": gin.H{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
		},
	})
}

func (h *Handler) emitUsage(provider, model string, inputTokens, outputTokens, totalTokens int, durationMs int64, userID string) {
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
		DurationMs:   durationMs,
	})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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

// Models returns available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	if h.apiKey != "" {
		models, err := inception.ListModels(h.apiKey, h.endpoint)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"models": models, "current": h.model})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"models":  []string{h.model},
		"current": h.model,
	})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "INCEPTION_MODEL":
		if h.apiKey != "" {
			models, err := inception.ListModels(h.apiKey, h.endpoint)
			if err != nil {
				log.Printf("ListModels error: %v", err)
				c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Failed to fetch models: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"options": models})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": []string{h.model}})

	default:
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Unknown field"})
	}
}

// FIMRequest is the body for POST /fim.
type FIMRequest struct {
	Prompt string `json:"prompt"`
	Suffix string `json:"suffix"`
}

// FIM handles a fill-in-the-middle autocomplete request using mercury-edit.
func (h *Handler) FIM(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "INCEPTION_API_KEY is not set."})
		return
	}

	var req FIMRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt required"})
		return
	}

	h.emitEvent("fim_request", fmt.Sprintf("prompt_len=%d suffix_len=%d", len(req.Prompt), len(req.Suffix)))

	start := time.Now()
	resp, err := inception.FIMCompletion(h.apiKey, h.endpoint, req.Prompt, req.Suffix)
	if err != nil {
		log.Printf("FIM error: %v", err)
		h.emitEvent("error", fmt.Sprintf("fim: %v", err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "FIM request failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)
	responseText := ""
	if len(resp.Choices) > 0 {
		responseText = resp.Choices[0].Message.Content
	}

	h.emitEvent("fim_response", fmt.Sprintf("tokens=%d+%d time=%dms", resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds()))

	c.JSON(http.StatusOK, gin.H{
		"response": responseText,
		"model":    "mercury-edit",
		"usage": gin.H{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
		},
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
