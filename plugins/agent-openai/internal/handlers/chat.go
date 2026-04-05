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

// chatRequest is the body for POST /chat.
type chatRequest struct {
	Message      string           `json:"message"`
	Model        string           `json:"model,omitempty"`
	ImageURLs    []string         `json:"image_urls,omitempty"`
	Conversation []openai.Message `json:"conversation"`
	WorkspaceID  string           `json:"workspace_id,omitempty"`
	AgentAlias   string           `json:"agent_alias,omitempty"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	SessionID    string           `json:"session_id,omitempty"`
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
		messages = []openai.Message{
			{Role: "user", Content: req.Message},
		}
	}

	// Attach image URLs to the last user message so both backends can use them.
	if len(req.ImageURLs) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				messages[i].ImageURLs = req.ImageURLs
				break
			}
		}
	}

	// Snapshot mutable config under read lock.
	h.mu.RLock()
	model := h.model
	apiKey := h.apiKey
	endpoint := h.endpoint
	backend := h.backend
	toolLoopLimit := h.toolLoopLimit
	debug := h.debug
	h.mu.RUnlock()

	// Use per-request model override if provided.
	if req.Model != "" {
		model = req.Model
	}

	// Validate workspace_id if provided.
	if req.WorkspaceID != "" && !isValidWorkspaceID(req.WorkspaceID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace_id: must be alphanumeric, hyphens, or underscores"})
		return
	}

	// Log incoming request.
	lastMsg := ""
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1].Content
	}
	if debug {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d workspace=%s content=%s", model, len(messages), req.WorkspaceID, truncateStr(lastMsg, 200)))
	} else {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d workspace=%s", model, len(messages), req.WorkspaceID))
	}

	start := time.Now()

	switch backend {
	case "subscription":
		if h.codexCLI == nil {
			h.emitEvent("error", "subscription backend not initialised")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Subscription backend is configured but Codex CLI was not initialised."})
			return
		}

		// Use injected system prompt (enriched by the relay) or fall back to embedded default.
		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}
		if systemPrompt != "" {
			filtered := make([]openai.Message, 0, len(messages))
			for _, m := range messages {
				if m.Role != "system" {
					filtered = append(filtered, m)
				}
			}
			messages = append([]openai.Message{{Role: "system", Content: systemPrompt}}, filtered...)
		}

		var workdir string
		if req.WorkspaceID != "" {
			workdir = "/workspaces/" + req.WorkspaceID
		}

		resp, err := h.codexCLI.ChatCompletion(model, messages, req.ImageURLs, workdir)
		if err != nil {
			log.Printf("Codex CLI error: %v", err)
			h.emitEvent("error", fmt.Sprintf("subscription: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Codex request failed: " + err.Error()})
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
			Backend:      "subscription",
		})
		h.emitUsage("openai", model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens, resp.Usage.CachedTokens, elapsed.Milliseconds(), userID)

		responseText := ""
		if len(resp.Choices) > 0 {
			responseText = resp.Choices[0].Message.Content
		}

		if debug {
			h.emitEvent("chat_response", fmt.Sprintf("backend=subscription model=%s tokens=%d+%d time=%dms response=%s",
				model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds(), truncateStr(responseText, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("backend=subscription model=%s tokens=%d+%d time=%dms len=%d",
				model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, elapsed.Milliseconds(), len(responseText)))
		}

		c.JSON(http.StatusOK, gin.H{
			"response": responseText,
			"model":    model,
			"backend":  "subscription",
			"usage": gin.H{
				"prompt_tokens":     resp.Usage.PromptTokens,
				"completion_tokens": resp.Usage.CompletionTokens,
			},
		})

	case "api_key":
		if apiKey == "" {
			h.emitEvent("error", "api_key backend has no OPENAI_API_KEY")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api_key backend is configured but OPENAI_API_KEY is not set."})
			return
		}

		// Discover available tools from registered tool:* plugins.
		tools := discoverTools(h.sdk)
		var toolDefs []openai.ToolDef
		if len(tools) > 0 {
			toolDefs = buildToolDefs(tools)
			h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
		}

		// Use injected system prompt (enriched by the relay) or fall back to embedded default.
		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}
		if systemPrompt != "" {
			filtered := make([]openai.Message, 0, len(messages))
			for _, m := range messages {
				if m.Role != "system" {
					filtered = append(filtered, m)
				}
			}
			messages = append([]openai.Message{{Role: "system", Content: systemPrompt}}, filtered...)
		}

		// Aggregate token usage across tool-use iterations.
		var totalInput, totalOutput int

		// Collect media attachments from tool results (images, etc.).
		var mediaAttachments []mediaAttachment

		// Tool-use loop: configurable iteration limit (0 = unrestricted).
		maxIter := toolLoopLimit
		for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
			var resp *openai.ChatResponse
			var err error

			if len(toolDefs) > 0 {
				resp, err = openai.ChatCompletionWithTools(apiKey, endpoint, model, messages, toolDefs)
			} else {
				resp, err = openai.ChatCompletion(apiKey, endpoint, model, messages)
			}
			if err != nil {
				log.Printf("OpenAI error: %v", err)
				h.emitEvent("error", fmt.Sprintf("openai: %v", err))
				c.JSON(http.StatusBadGateway, gin.H{"error": "OpenAI request failed: " + err.Error()})
				return
			}

			totalInput += resp.Usage.PromptTokens
			totalOutput += resp.Usage.CompletionTokens

			if len(resp.Choices) == 0 {
				c.JSON(http.StatusBadGateway, gin.H{"error": "OpenAI returned no choices"})
				return
			}

			choice := resp.Choices[0]

			// If the model wants to call tools, execute them and loop.
			if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
				if maxIter > 0 && iteration == maxIter {
					h.emitEvent("tool_loop", "max iterations reached, returning last response")
					break
				}

				// Append the assistant message with tool calls to the conversation.
				messages = append(messages, openai.Message{
					Role:      "assistant",
					ToolCalls: choice.Message.ToolCalls,
				})

				// Execute each tool call and append results.
				for _, tc := range choice.Message.ToolCalls {
					h.emitEvent("tool_call", fmt.Sprintf("tool=%s args=%s", tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))

					result, err := executeToolCall(h.sdk, tools, tc)
					if err != nil {
						log.Printf("Tool call %s failed: %v", tc.Function.Name, err)
						h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", tc.Function.Name, err))
						result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
					} else {
						h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
						// Extract any embedded image data before sending to OpenAI.
						cleaned, atts := processToolResultMedia(result)
						if len(atts) > 0 {
							mediaAttachments = append(mediaAttachments, atts...)
							result = cleaned
						}
					}

					messages = append(messages, openai.Message{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    result,
					})
				}

				continue // Loop back to call OpenAI with tool results.
			}

			// finish_reason == "stop" or no tool calls — return the final response.
			elapsed := time.Since(start)

			h.usage.RecordRequest(usage.RequestRecord{
				Model:        model,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				TotalTokens:  totalInput + totalOutput,
				DurationMs:   elapsed.Milliseconds(),
				Backend:      "api_key",
			})
			h.emitUsage("openai", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)

			responseText := choice.Message.Content
			if debug {
				h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms response=%s",
					model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(responseText, 200)))
			} else {
				h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms len=%d",
					model, totalInput, totalOutput, elapsed.Milliseconds(), len(responseText)))
			}

			result := gin.H{
				"response": responseText,
				"model":    model,
				"backend":  "api_key",
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

		// Fallback if we exhausted iterations without a stop.
		elapsed := time.Since(start)
		h.emitUsage("openai", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)
		c.JSON(http.StatusOK, gin.H{
			"response": "I attempted to use tools but reached the maximum number of iterations. Please try again with a simpler request.",
			"model":    model,
			"backend":  "api_key",
			"usage": gin.H{
				"prompt_tokens":     totalInput,
				"completion_tokens": totalOutput,
			},
		})

	default:
		h.emitEvent("error", fmt.Sprintf("unknown backend: %s", backend))
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unknown backend %q. Set OPENAI_BACKEND to 'subscription' or 'api_key'.", backend)})
	}
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
