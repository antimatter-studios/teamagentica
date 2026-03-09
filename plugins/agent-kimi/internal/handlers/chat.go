package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimi"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/usage"
)

type Handler struct {
	apiKey        string
	model         string
	toolLoopLimit int
	debug         bool
	sdk           *pluginsdk.Client
	client        *kimi.Client
	usage         *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey:        apiKey,
		model:         model,
		toolLoopLimit: 20,
		debug:         debug,
		client:        kimi.NewClient(apiKey, debug),
		usage:         usage.NewTracker(dataPath),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
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

type chatRequest struct {
	Message       string         `json:"message"`
	Model         string         `json:"model,omitempty"`
	Conversation  []kimi.Message `json:"conversation"`
	IsCoordinator bool           `json:"is_coordinator,omitempty"`
	AgentAlias    string         `json:"agent_alias,omitempty"`
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
		messages = []kimi.Message{
			{Role: "user", Content: req.Message},
		}
	}

	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set KIMI_API_KEY."})
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

	// Discover available tools from registered tool:* plugins.
	tools := discoverTools(h.sdk)
	var toolDefs []kimi.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	// Build agent's own system prompt.
	systemPrompt := buildSystemPrompt(h.sdk, req.IsCoordinator, req.AgentAlias, tools)
	if systemPrompt != "" {
		filtered := make([]kimi.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]kimi.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment

	// Tool-use loop.
	maxIter := h.toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		resp, err := h.client.ChatCompletion(model, messages, toolDefs...)
		if err != nil {
			log.Printf("Kimi error: %v", err)
			h.emitEvent("error", fmt.Sprintf("kimi: %v", err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Kimi request failed: " + err.Error()})
			return
		}

		totalInput += resp.Usage.PromptTokens
		totalOutput += resp.Usage.CompletionTokens

		// If the model wants to call tools, execute them and loop.
		if resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0 {
			if maxIter > 0 && iteration == maxIter {
				h.emitEvent("tool_loop", "max iterations reached, returning last response")
				break
			}

			messages = append(messages, kimi.Message{
				Role:      "assistant",
				ToolCalls: resp.ToolCalls,
			})

			for _, tc := range resp.ToolCalls {
				h.emitEvent("tool_call", fmt.Sprintf("tool=%s args=%s", tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))

				result, err := executeToolCall(h.sdk, tools, tc)
				if err != nil {
					log.Printf("Tool call %s failed: %v", tc.Function.Name, err)
					h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", tc.Function.Name, err))
					result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
					cleaned, att := processToolResultMedia(result)
					if att != nil {
						mediaAttachments = append(mediaAttachments, *att)
						result = cleaned
					}
				}

				messages = append(messages, kimi.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}

			continue
		}

		// Final response.
		elapsed := time.Since(start)

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:       userID,
				Provider:     "moonshot",
				Model:        model,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				TotalTokens:  totalInput + totalOutput,
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
			"backend":  "kimi",
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
			Provider:     "moonshot",
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
		"backend":  "kimi",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
		},
	})
}

// SystemPrompt returns the system prompts this agent would use in coordinator and direct modes.
func (h *Handler) SystemPrompt(c *gin.Context) {
	tools := discoverTools(h.sdk)
	c.JSON(http.StatusOK, gin.H{
		"system_prompt_coordinator": buildSystemPrompt(h.sdk, true, "", tools),
		"system_prompt_direct":      buildSystemPrompt(h.sdk, false, "this-agent", tools),
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

	systemPrompt := buildSystemPrompt(h.sdk, true, "", tools)

	c.JSON(http.StatusOK, gin.H{
		"tools":                    entries,
		"system_prompt_coordinator": systemPrompt,
		"system_prompt_direct":     buildSystemPrompt(h.sdk, false, "example", tools),
	})
}

// Models returns the list of available models and the current default.
func (h *Handler) Models(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusOK, gin.H{
			"models":  kimi.DefaultModels(),
			"current": h.model,
		})
		return
	}

	models, _, err := h.client.ListModels()
	if err != nil {
		log.Printf("ListModels error: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"models":  kimi.DefaultModels(),
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
