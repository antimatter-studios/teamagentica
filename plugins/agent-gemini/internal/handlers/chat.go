package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
)

type Handler struct {
	mu            sync.RWMutex
	apiKey        string
	model         string
	toolLoopLimit int
	debug         bool
	defaultPrompt string
	sdk           *pluginsdk.Client
	client        *gemini.Client
	usage         *usage.Tracker
}

// HandlerConfig holds the parameters for constructing a Handler.
type HandlerConfig struct {
	APIKey              string
	Model               string
	Debug               bool
	DataPath            string
	DefaultSystemPrompt string
}

// NewHandler creates a new Handler from the given config.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		apiKey:        cfg.APIKey,
		model:         cfg.Model,
		toolLoopLimit: 20,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultSystemPrompt,
		client:        gemini.NewClient(cfg.APIKey, cfg.Debug),
		usage:         usage.NewTracker(cfg.DataPath),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (h *Handler) ApplyConfig(config map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := config["GEMINI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s → %s", h.model, v)
		h.model = v
	}
	rebuildClient := false
	if v, ok := config["GEMINI_API_KEY"]; ok {
		if v != h.apiKey {
			h.apiKey = v
			rebuildClient = true
		}
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		newDebug := v == "true"
		if newDebug != h.debug {
			h.debug = newDebug
			rebuildClient = true
		}
	}
	if rebuildClient {
		h.client = gemini.NewClient(h.apiKey, h.debug)
		log.Printf("[config] rebuilt gemini client (debug=%v)", h.debug)
	}
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "agent-gemini",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
		"model":      h.model,
	})
}

type chatRequest struct {
	Message       string           `json:"message"`
	Model         string           `json:"model,omitempty"`
	ImageURLs     []string         `json:"image_urls,omitempty"`
	Conversation  []gemini.Message `json:"conversation"`
	AgentAlias    string           `json:"agent_alias,omitempty"`
	SystemPrompt  string           `json:"system_prompt,omitempty"`
}

func (h *Handler) Chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []gemini.Message{
			{Role: "user", Content: req.Message},
		}
	}

	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set GEMINI_API_KEY."})
		return
	}

	// Use per-request model override if provided.
	model := h.model
	if req.Model != "" {
		model = req.Model
	}

	lastMsg := ""
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1].Content
	}
	if h.debug {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d content=%s", model, len(messages), truncateStr(lastMsg, 200)))
	} else {
		h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d", model, len(messages)))
	}

	start := time.Now()

	// Discover available tools.
	tools := discoverTools(h.sdk)
	var toolDefs []gemini.FunctionDeclaration
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
		filtered := make([]gemini.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]gemini.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment

	maxIter := h.toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		var resp *gemini.ChatResponse
		var err error

		if len(toolDefs) > 0 {
			resp, err = h.client.ChatCompletionWithTools(model, messages, toolDefs, req.ImageURLs...)
		} else {
			resp, err = h.client.ChatCompletion(model, messages, req.ImageURLs...)
		}
		if err != nil {
			log.Printf("Gemini error: %v", err)
			h.emitEvent("error", fmt.Sprintf("gemini: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Gemini request failed: " + err.Error()})
			return
		}

		totalInput += resp.Usage.PromptTokens
		totalOutput += resp.Usage.CompletionTokens

		// If the model wants to call a function, execute it and loop.
		if resp.FunctionCall != nil {
			if maxIter > 0 && iteration == maxIter {
				h.emitEvent("tool_loop", "max iterations reached")
				break
			}

			fc := resp.FunctionCall
			h.emitEvent("tool_call", fmt.Sprintf("tool=%s", fc.Name))

			// Append model's function call to conversation.
			messages = append(messages, gemini.Message{
				FunctionCallName: fc.Name,
				FunctionCallArgs: fc.Args,
			})

			result, execErr := executeToolCall(h.sdk, tools, fc.Name, fc.Args)
			if execErr != nil {
				log.Printf("Tool call %s failed: %v", fc.Name, execErr)
				h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", fc.Name, execErr))
				result = fmt.Sprintf(`{"error": "%s"}`, execErr.Error())
			} else {
				h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", fc.Name, len(result)))
				cleaned, atts := processToolResultMedia(result)
				if len(atts) > 0 {
					mediaAttachments = append(mediaAttachments, atts...)
					result = cleaned
				}
			}

			// Parse result as JSON for function response.
			var resultData map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(result), &resultData); jsonErr != nil {
				resultData = map[string]interface{}{"result": result}
			}

			messages = append(messages, gemini.Message{
				FunctionRespName: fc.Name,
				FunctionRespData: resultData,
			})

			// Only send images on the first iteration.
			req.ImageURLs = nil
			continue
		}

		// Final text response — no more tool calls.
		elapsed := time.Since(start)

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			CachedTokens: resp.Usage.CachedTokens,
			DurationMs:   elapsed.Milliseconds(),
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:       userID,
				Provider:     "gemini",
				Model:        model,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				TotalTokens:  totalInput + totalOutput,
				CachedTokens: resp.Usage.CachedTokens,
				DurationMs:   elapsed.Milliseconds(),
			})
		}

		if h.debug {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
				model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(resp.Content, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
				model, totalInput, totalOutput, elapsed.Milliseconds(), len(resp.Content)))
		}

		result := gin.H{
			"response": resp.Content,
			"model":    model,
			"backend":  "gemini",
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

	// Fallback if we exhausted iterations.
	elapsed := time.Since(start)
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:       userID,
			Provider:     "gemini",
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"response": "I attempted to use tools but reached the maximum number of iterations. Please try again with a simpler request.",
		"model":    model,
		"backend":  "gemini",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
		},
	})
}

// SystemPrompt returns the system prompt this agent would use.
func (h *Handler) SystemPrompt(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"default_prompt": h.defaultPrompt,
	})
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
			"models":  gemini.DefaultModels(),
			"current": h.model,
		})
		return
	}

	models, _, err := h.client.ListModels()
	if err != nil {
		log.Printf("ListModels error: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"models":  gemini.DefaultModels(),
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
	case "GEMINI_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "No API key configured."})
			return
		}
		models, fallback, err := h.client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
			c.JSON(http.StatusOK, gin.H{"options": models, "fallback": fallback, "error": "Failed to fetch models: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": models, "fallback": fallback})
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
