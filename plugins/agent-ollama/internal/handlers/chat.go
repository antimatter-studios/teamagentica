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
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/ollama"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/usage"
)

// Handler holds the plugin's configuration and exposes HTTP handlers.
type Handler struct {
	mu            sync.RWMutex
	model         string
	modelList     []string // currently configured models
	endpoint      string   // Ollama API base URL (e.g. http://localhost:11434)
	toolLoopLimit int
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	usage         *usage.Tracker
}

// HandlerConfig holds parameters for constructing a Handler.
type HandlerConfig struct {
	Model               string
	Endpoint            string
	ToolLoopLimit       int
	Debug               bool
	DataPath            string
	DefaultSystemPrompt string
}

// NewHandler creates a new Handler.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		model:         cfg.Model,
		endpoint:      cfg.Endpoint,
		toolLoopLimit: cfg.ToolLoopLimit,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultSystemPrompt,
		usage:         usage.NewTracker(cfg.DataPath),
	}
}

// SetSDK attaches the plugin SDK client.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) { h.sdk = sdk }

// SetModelList sets the initial model list (called at startup).
func (h *Handler) SetModelList(models []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.modelList = models
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

// ApplyConfig updates mutable config fields in-place.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["OLLAMA_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
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
	if v, ok := config["OLLAMA_MODELS"]; ok && v != "" {
		var newModels []string
		if err := json.Unmarshal([]byte(v), &newModels); err == nil {
			oldModels := h.modelList
			h.modelList = newModels
			endpoint := h.endpoint

			// Diff: find models to pull and models to delete.
			oldSet := make(map[string]bool, len(oldModels))
			for _, m := range oldModels {
				oldSet[m] = true
			}
			newSet := make(map[string]bool, len(newModels))
			for _, m := range newModels {
				newSet[m] = true
			}

			var toPull, toDelete []string
			for _, m := range newModels {
				if m != "" && !oldSet[m] {
					toPull = append(toPull, m)
				}
			}
			for _, m := range oldModels {
				if m != "" && !newSet[m] {
					toDelete = append(toDelete, m)
				}
			}

			if len(toPull) > 0 || len(toDelete) > 0 {
				go func() {
					for _, m := range toPull {
						log.Printf("[models] pulling %s...", m)
						if err := ollama.PullModel(endpoint, m); err != nil {
							log.Printf("[models] pull %s failed: %v", m, err)
						} else {
							log.Printf("[models] %s ready", m)
						}
					}
					for _, m := range toDelete {
						log.Printf("[models] deleting %s...", m)
						if err := ollama.DeleteModel(endpoint, m); err != nil {
							log.Printf("[models] delete %s failed: %v", m, err)
						} else {
							log.Printf("[models] %s deleted", m)
						}
					}
				}()
			}
		}
	}
}

// Health returns a health check.
func (h *Handler) Health(c *gin.Context) {
	h.mu.RLock()
	endpoint := h.endpoint
	h.mu.RUnlock()

	err := ollama.Healthy(endpoint)
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-ollama",
		"configured": err == nil,
		"backend":    "ollama",
	})
}

// chatRequest is the body for POST /chat.
type chatRequest struct {
	Message      string           `json:"message"`
	Model        string           `json:"model,omitempty"`
	Conversation []ollama.Message `json:"conversation"`
	WorkspaceID  string           `json:"workspace_id,omitempty"`
	AgentAlias   string           `json:"agent_alias,omitempty"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
}

// Chat handles a non-streaming chat completion request with tool loop.
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
		messages = []ollama.Message{{Role: "user", Content: req.Message}}
	}

	h.mu.RLock()
	model := h.model
	endpoint := h.endpoint
	toolLoopLimit := h.toolLoopLimit
	debug := h.debug
	h.mu.RUnlock()

	if req.Model != "" {
		model = req.Model
	}

	// System prompt.
	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = h.defaultPrompt
	}
	if systemPrompt != "" {
		filtered := make([]ollama.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]ollama.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}

	lastMsg := ""
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1].Content
	}
	if debug {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d content=%s", model, len(messages), truncateStr(lastMsg, 200)))
	} else {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d", model, len(messages)))
	}

	start := time.Now()

	// Discover tools.
	tools := discoverTools(h.sdk)
	var toolDefs []ollama.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment

	maxIter := toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		var resp *ollama.ChatResponse
		var err error

		if len(toolDefs) > 0 {
			resp, err = ollama.ChatCompletionWithTools(endpoint, model, messages, toolDefs)
		} else {
			resp, err = ollama.ChatCompletion(endpoint, model, messages)
		}
		if err != nil {
			log.Printf("Ollama error: %v", err)
			h.emitEvent("error", fmt.Sprintf("ollama: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Ollama request failed: " + err.Error()})
			return
		}

		totalInput += resp.Usage.PromptTokens
		totalOutput += resp.Usage.CompletionTokens

		if len(resp.Choices) == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Ollama returned no choices"})
			return
		}

		choice := resp.Choices[0]

		if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
			if maxIter > 0 && iteration == maxIter {
				h.emitEvent("tool_loop", "max iterations reached")
				break
			}

			messages = append(messages, ollama.Message{
				Role:      "assistant",
				ToolCalls: choice.Message.ToolCalls,
			})

			for _, tc := range choice.Message.ToolCalls {
				h.emitEvent("tool_call", fmt.Sprintf("tool=%s args=%s", tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))

				result, err := executeToolCall(h.sdk, tools, tc)
				if err != nil {
					log.Printf("Tool call %s failed: %v", tc.Function.Name, err)
					h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", tc.Function.Name, err))
					result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
					cleaned, atts := processToolResultMedia(result)
					if len(atts) > 0 {
						mediaAttachments = append(mediaAttachments, atts...)
						result = cleaned
					}
				}

				messages = append(messages, ollama.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
			continue
		}

		// Finished — return response.
		elapsed := time.Since(start)

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "ollama",
		})
		h.emitUsage("ollama", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)

		responseText := choice.Message.Content
		if debug {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
				model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(responseText, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
				model, totalInput, totalOutput, elapsed.Milliseconds(), len(responseText)))
		}

		result := gin.H{
			"response": responseText,
			"model":    model,
			"backend":  "ollama",
			"usage": gin.H{
				"prompt_tokens":     totalInput,
				"completion_tokens": totalOutput,
			},
		}
		if len(mediaAttachments) > 0 {
			result["attachments"] = mediaAttachments
		}
		c.JSON(http.StatusOK, result)
		return
	}

	// Fallback.
	elapsed := time.Since(start)
	h.emitUsage("ollama", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)
	c.JSON(http.StatusOK, gin.H{
		"response": "I attempted to use tools but reached the maximum number of iterations.",
		"model":    model,
		"backend":  "ollama",
		"usage":    gin.H{"prompt_tokens": totalInput, "completion_tokens": totalOutput},
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

// SystemPrompt returns the system prompt, plus rendered previews for
// every persona/alias that routes through this plugin.
func (h *Handler) SystemPrompt(c *gin.Context) {
	resp := gin.H{"default_prompt": h.defaultPrompt}

	if h.sdk != nil {
		if previews, err := h.sdk.SystemPromptPreview(h.sdk.PluginID(), h.defaultPrompt); err == nil && len(previews) > 0 {
			resp["aliases"] = previews
		}
	}

	c.JSON(http.StatusOK, resp)
}

// DiscoveredTools returns tools this agent has discovered.
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
	c.JSON(http.StatusOK, gin.H{"tools": entries})
}

// Models returns available models from Ollama.
func (h *Handler) Models(c *gin.Context) {
	h.mu.RLock()
	endpoint := h.endpoint
	model := h.model
	h.mu.RUnlock()

	models, err := ollama.ListModels(endpoint)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"models": []string{model}, "current": model})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "current": model})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	h.mu.RLock()
	endpoint := h.endpoint
	model := h.model
	h.mu.RUnlock()

	switch field {
	case "OLLAMA_MODEL":
		models, err := ollama.ListModels(endpoint)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"options": []string{model}, "error": err.Error()})
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

// UsageRecords returns raw request records.
func (h *Handler) UsageRecords(c *gin.Context) {
	since := c.Query("since")
	records := h.usage.Records(since)
	if records == nil {
		records = []usage.RequestRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}

// PullModel pulls a single model on demand.
func (h *Handler) PullModel(c *gin.Context) {
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model required"})
		return
	}

	h.mu.RLock()
	endpoint := h.endpoint
	h.mu.RUnlock()

	log.Printf("[models] pulling %s...", req.Model)
	h.emitEvent("model_pull", fmt.Sprintf("pulling %s", req.Model))

	if err := ollama.PullModel(endpoint, req.Model); err != nil {
		log.Printf("[models] pull failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[models] %s ready", req.Model)
	h.emitEvent("model_pull", fmt.Sprintf("%s ready", req.Model))
	c.JSON(http.StatusOK, gin.H{"status": "ok", "model": req.Model})
}

// DeleteModel deletes a single model from Ollama.
func (h *Handler) DeleteModel(c *gin.Context) {
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model required"})
		return
	}

	h.mu.RLock()
	endpoint := h.endpoint
	h.mu.RUnlock()

	log.Printf("[models] deleting %s...", req.Model)
	h.emitEvent("model_delete", fmt.Sprintf("deleting %s", req.Model))

	if err := ollama.DeleteModel(endpoint, req.Model); err != nil {
		log.Printf("[models] delete failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[models] %s deleted", req.Model)
	h.emitEvent("model_delete", fmt.Sprintf("%s deleted", req.Model))
	c.JSON(http.StatusOK, gin.H{"status": "ok", "model": req.Model})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
