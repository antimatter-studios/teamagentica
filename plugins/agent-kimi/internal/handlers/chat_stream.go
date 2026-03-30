package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimi"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimicli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/usage"
)

// ChatStream handles a streaming chat completion request.
// Writes SSE events to the client as tokens arrive from the Kimi API.
//
// SSE event types:
//   - token:       {"content": "..."}
//   - tool_call:   {"name": "...", "arguments": "..."}
//   - tool_result: {"name": "...", "result": "...", "error": "..."}
//   - done:        {"response": "...", "model": "...", "backend": "...", "usage": {...}, "attachments": [...]}
//   - error:       {"error": "..."}
func (h *Handler) ChatStream(c *gin.Context) {
	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	h.mu.RLock()
	kimiCLI := h.kimiCLI
	mcpConfigFile := h.mcpConfigFile
	apiKey := h.apiKey
	model := h.model
	toolLoopLimit := h.toolLoopLimit
	debug := h.debug
	h.mu.RUnlock()

	if kimiCLI == nil && apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set KIMI_API_KEY."})
		return
	}

	if req.Model != "" {
		model = req.Model
	}

	start := time.Now()

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []kimi.Message{{Role: "user", Content: req.Message}}
	}

	// System prompt.
	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = h.defaultPrompt
	}
	if systemPrompt != "" {
		filtered := make([]kimi.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]kimi.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	writeSSE := func(event string, data interface{}) {
		jsonData, err := json.Marshal(data)
		if err != nil {
			log.Printf("[stream] failed to marshal SSE data: %v", err)
			return
		}
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, jsonData)
		c.Writer.Flush()
	}

	writeError := func(msg string) {
		writeSSE("error", gin.H{"error": msg})
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// CLI backend: delegate to kimi-cli subprocess (handles tools via MCP).
	if kimiCLI != nil {
		h.chatStreamCLI(ctx, c, kimiCLI, mcpConfigFile, model, messages, debug, userID, start, writeSSE, writeError)
		return
	}

	// API backend: discover tools and call Moonshot API directly.
	tools := discoverTools(h.sdk)
	var toolDefs []kimi.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment
	var fullResponse string

	maxIter := toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		stream := h.client.ChatCompletionStream(ctx, model, messages, toolDefs...)

		var iterContent string
		var iterReasoning string
		var iterToolCalls []kimi.ToolCall

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] Kimi error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("kimi stream: %v", ev.Err))
				writeError("Kimi stream error: " + ev.Err.Error())
				return
			}

			if ev.Token != "" {
				iterContent += ev.Token
				writeSSE("token", gin.H{"content": ev.Token})
			}

			if ev.ReasoningContent != "" {
				iterReasoning += ev.ReasoningContent
			}

			if len(ev.ToolCalls) > 0 {
				iterToolCalls = kimi.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
			}

			if ev.Usage != nil {
				totalInput += ev.Usage.PromptTokens
				totalOutput += ev.Usage.CompletionTokens
			}

			if ev.FinishReason == "stop" {
				fullResponse += iterContent
				goto done
			}

			if ev.FinishReason == "tool_calls" {
				break
			}
		}

		if len(iterToolCalls) > 0 {
			if maxIter > 0 && iteration == maxIter {
				h.emitEvent("tool_loop", "max iterations reached during streaming")
				fullResponse += iterContent
				goto done
			}

			fullResponse += iterContent

			messages = append(messages, kimi.Message{
				Role:             "assistant",
				Content:          iterContent,
				ReasoningContent: iterReasoning,
				ToolCalls:        iterToolCalls,
			})

			for _, tc := range iterToolCalls {
				h.emitEvent("tool_call", fmt.Sprintf("tool=%s args=%s", tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))
				writeSSE("tool_call", gin.H{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})

				result, err := executeToolCall(h.sdk, tools, tc)
				if err != nil {
					log.Printf("[stream] Tool call %s failed: %v", tc.Function.Name, err)
					h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", tc.Function.Name, err))
					result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
					writeSSE("tool_result", gin.H{
						"name":  tc.Function.Name,
						"error": err.Error(),
					})
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
					cleaned, atts := processToolResultMedia(result)
					if len(atts) > 0 {
						mediaAttachments = append(mediaAttachments, atts...)
						result = cleaned
					}
					writeSSE("tool_result", gin.H{
						"name":   tc.Function.Name,
						"result": truncateStr(result, 500),
					})
				}

				messages = append(messages, kimi.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}

			continue
		}

		fullResponse += iterContent
		break
	}

done:
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

	if debug {
		h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
			model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
	} else {
		h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
			model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
	}

	doneData := gin.H{
		"response": fullResponse,
		"model":    model,
		"backend":  "kimi",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
		},
	}
	if len(mediaAttachments) > 0 {
		doneData["attachments"] = mediaAttachments
	}
	writeSSE("done", doneData)
}

// chatStreamCLI handles streaming via the Kimi CLI subprocess.
func (h *Handler) chatStreamCLI(ctx context.Context, c *gin.Context, cli *kimicli.Client, mcpConfigFile, model string, messages []kimi.Message, debug bool, userID string, start time.Time, writeSSE func(string, interface{}), writeError func(string)) {
	// Build a prompt string from the conversation messages.
	prompt := buildPrompt(messages)

	stream := cli.ChatCompletionStream(ctx, model, prompt, mcpConfigFile)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Kimi CLI error: %v", ev.Err)
			h.emitEvent("error", fmt.Sprintf("kimi-cli stream: %v", ev.Err))
			writeError("Kimi CLI error: " + ev.Err.Error())
			return
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			writeSSE("token", gin.H{"content": ev.Text})
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.InputTokens
			totalOutput = ev.Usage.OutputTokens
		}
	}

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

	if debug {
		h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d time=%dms response=%s",
			model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
	} else {
		h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d time=%dms len=%d",
			model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
	}

	writeSSE("done", gin.H{
		"response": fullResponse,
		"model":    model,
		"backend":  "cli",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
		},
	})
}

// buildPrompt concatenates conversation messages into a single prompt string.
func buildPrompt(messages []kimi.Message) string {
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
