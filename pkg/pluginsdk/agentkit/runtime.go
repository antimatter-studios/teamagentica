package agentkit

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// agentHandler implements pluginsdk.AgentProvider using a ProviderAdapter.
// It handles the tool loop, system prompt, tool discovery, usage reporting,
// and streaming — the adapter only needs to call the LLM API.
type agentHandler struct {
	client  *pluginsdk.Client
	adapter ProviderAdapter
	config  Config

	// defaultPrompt is the fallback system prompt when the request doesn't provide one.
	defaultPrompt string
}

// RegisterAgentChat registers the standard agent routes on the given router.
// This replaces hundreds of lines of boilerplate in each agent plugin.
//
// Routes registered:
//   - POST /chat (SSE streaming, handled by pluginsdk.RegisterAgentChat)
//   - GET /health
//   - GET /mcp (discovered tools)
//
// The handler implements pluginsdk.AgentProvider and drives the tool loop
// automatically: discover tools -> call adapter.StreamChat -> execute tool
// calls -> feed results back -> repeat until done or max loops.
func RegisterAgentChat(router *gin.Engine, client *pluginsdk.Client, adapter ProviderAdapter, defaultPrompt string, opts ...Option) {
	cfg := defaultConfig()

	// Apply adapter's default model if not overridden.
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = adapter.ModelID()
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	h := &agentHandler{
		client:        client,
		adapter:       adapter,
		config:        cfg,
		defaultPrompt: defaultPrompt,
	}

	// Register POST /chat via the existing SDK SSE handler.
	pluginsdk.RegisterAgentChat(router, h)

	// Health check.
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":   "ok",
			"provider": adapter.ProviderName(),
			"model":    cfg.DefaultModel,
		})
	})

	// Discovered tools endpoint.
	router.GET("/mcp", func(c *gin.Context) {
		tools := DiscoverTools(client)
		type entry struct {
			Name        string      `json:"name"`
			Description string      `json:"description"`
			Endpoint    string      `json:"endpoint,omitempty"`
			Parameters  interface{} `json:"parameters"`
			PluginID    string      `json:"plugin_id,omitempty"`
		}
		var entries []entry
		for _, t := range tools {
			entries = append(entries, entry{
				Name:        t.PrefixedName,
				Description: t.Description,
				Endpoint:    t.Endpoint,
				Parameters:  t.Parameters,
				PluginID:    t.PluginID,
			})
		}
		c.JSON(200, gin.H{"tools": entries})
	})


}

// ChatStream implements pluginsdk.AgentProvider. It drives the full tool loop:
//  1. Build system prompt
//  2. Discover available MCP tools
//  3. Call adapter.StreamChat
//  4. If tool_use: execute tools, append results, go to 3
//  5. Emit done event with full response and usage
func (h *agentHandler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		start := time.Now()
		model := h.config.DefaultModel
		if req.Model != "" {
			model = req.Model
		}

		// Build messages from the SDK request.
		messages := convertSDKMessages(req)

		// System prompt: request overrides default.
		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}

		// Discover tools (includes workspace tools registered by workspace-manager).
		discoveredTools := DiscoverTools(h.client)
		toolDefs := ToToolDefinitions(discoveredTools)

		if len(toolDefs) > 0 && h.config.Debug {
			log.Printf("agentkit: %d tools available for %s", len(toolDefs), h.adapter.ProviderName())
		}

		sink := newChannelSink(ch)

		var totalUsage Usage
		var totalCost float64
		var lastNumTurns int
		var lastSessionID string
		var mediaAttachments []pluginsdk.AgentAttachment

		maxLoops := h.config.MaxToolLoops
		if maxLoops <= 0 {
			maxLoops = 10
		}

		for iteration := 0; iteration <= maxLoops; iteration++ {
			provReq := ProviderRequest{
				Messages:     messages,
				SystemPrompt: systemPrompt,
				Tools:        toolDefs,
				Model:        model,
				MaxTokens:    h.config.MaxTokens,
				Temperature:  h.config.Temperature,
				WorkspaceID:  req.WorkspaceID,
				SessionID:    req.SessionID,
			}

			result, err := h.adapter.StreamChat(ctx, provReq, sink)
			if err != nil {
				log.Printf("agentkit: StreamChat error: %v", err)
				ch <- pluginsdk.StreamError(fmt.Sprintf("%s error: %v", h.adapter.ProviderName(), err))
				return
			}

			// Accumulate usage and provider metadata.
			totalUsage.InputTokens += result.Usage.InputTokens
			totalUsage.OutputTokens += result.Usage.OutputTokens
			totalUsage.CachedTokens += result.Usage.CachedTokens
			totalCost += result.CostUSD
			lastNumTurns = result.NumTurns
			if result.SessionID != "" {
				lastSessionID = result.SessionID
			}

			// If no tool calls, we're done.
			if result.FinishReason != FinishReasonToolUse || len(result.ToolCalls) == 0 {
				break
			}

			// Guard against exceeding max loops on the next iteration.
			if iteration == maxLoops {
				log.Printf("agentkit: max tool loops (%d) reached", maxLoops)
				break
			}

			// Append assistant message with tool calls.
			messages = append(messages, Message{
				Role:      "assistant",
				ToolCalls: result.ToolCalls,
			})

			// Execute each tool call.
			for _, tc := range result.ToolCalls {
				if h.config.Debug {
					log.Printf("agentkit: tool_call %s args=%s", tc.Name, truncate(string(tc.Arguments), 200))
				}

				toolResult, execErr := ExecuteToolCall(h.client, discoveredTools, tc)
				isError := false
				if execErr != nil {
					log.Printf("agentkit: tool %s failed: %v", tc.Name, execErr)
					toolResult = fmt.Sprintf(`{"error": "%s"}`, execErr.Error())
					isError = true
					ch <- pluginsdk.StreamToolError(tc.Name, execErr.Error())
				} else {
					// Check for media attachments in the result.
					cleaned, atts := ProcessToolResultMedia(toolResult)
					if len(atts) > 0 {
						mediaAttachments = append(mediaAttachments, atts...)
						toolResult = cleaned
					}
					ch <- pluginsdk.StreamToolResult(tc.Name, truncate(toolResult, 500))
				}

				// Append tool result message.
				messages = append(messages, Message{
					Role: "tool",
					ToolResult: &ToolResult{
						CallID:  tc.ID,
						Content: toolResult,
						IsError: isError,
					},
				})
			}
		}

		elapsed := time.Since(start)

		// Report usage via SDK.
		if h.client != nil {
			h.client.ReportUsage(pluginsdk.UsageReport{
				UserID:       req.UserID,
				Provider:     h.adapter.ProviderName(),
				Model:        model,
				InputTokens:  totalUsage.InputTokens,
				OutputTokens: totalUsage.OutputTokens,
				TotalTokens:  totalUsage.InputTokens + totalUsage.OutputTokens,
				CachedTokens: totalUsage.CachedTokens,
				DurationMs:   elapsed.Milliseconds(),
			})
		}

		fullResponse := sink.FullResponse()

		if h.config.Debug {
			log.Printf("agentkit: %s model=%s tokens=%d+%d time=%dms len=%d cost=%.6f turns=%d session=%s",
				h.adapter.ProviderName(), model,
				totalUsage.InputTokens, totalUsage.OutputTokens,
				elapsed.Milliseconds(), len(fullResponse), totalCost, lastNumTurns, lastSessionID)
		}

		// Emit done event.
		doneEvent := pluginsdk.DoneEvent{
			Response:  fullResponse,
			Model:     model,
			Backend:   h.adapter.ProviderName(),
			CostUSD:   totalCost,
			NumTurns:  lastNumTurns,
			SessionID: lastSessionID,
			Usage: &pluginsdk.AgentUsage{
				PromptTokens:     totalUsage.InputTokens,
				CompletionTokens: totalUsage.OutputTokens,
				CachedTokens:     totalUsage.CachedTokens,
			},
		}
		if len(mediaAttachments) > 0 {
			doneEvent.Attachments = mediaAttachments
		}
		ch <- pluginsdk.StreamDone(doneEvent)
	}()

	return ch
}

// convertSDKMessages converts pluginsdk.AgentChatRequest messages to agentkit Messages.
func convertSDKMessages(req pluginsdk.AgentChatRequest) []Message {
	if len(req.Conversation) > 0 {
		msgs := make([]Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msgs[i] = Message{Role: m.Role, Content: m.Content}
		}
		return msgs
	}
	return []Message{{Role: "user", Content: req.Message}}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
