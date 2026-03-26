package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/ollama"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/usage"
)

// ChatStream handles a streaming chat completion request.
func (h *Handler) ChatStream(c *gin.Context) {
	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
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

	start := time.Now()

	messages := req.Conversation
	if len(messages) == 0 {
		messages = []ollama.Message{{Role: "user", Content: req.Message}}
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

	// Discover tools.
	tools := discoverTools(h.sdk)
	var toolDefs []ollama.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment
	var fullResponse string

	maxIter := toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		stream := ollama.ChatCompletionStream(ctx, endpoint, model, messages, toolDefs)

		var iterContent string
		var iterToolCalls []ollama.ToolCall

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] Ollama error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("ollama stream: %v", ev.Err))
				writeError("Ollama stream error: " + ev.Err.Error())
				return
			}

			if ev.Token != "" {
				iterContent += ev.Token
				writeSSE("token", gin.H{"content": ev.Token})
			}

			if len(ev.ToolCalls) > 0 {
				iterToolCalls = ollama.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
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

			messages = append(messages, ollama.Message{
				Role:      "assistant",
				Content:   iterContent,
				ToolCalls: iterToolCalls,
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
					writeSSE("tool_result", gin.H{"name": tc.Function.Name, "error": err.Error()})
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
					cleaned, atts := processToolResultMedia(result)
					if len(atts) > 0 {
						mediaAttachments = append(mediaAttachments, atts...)
						result = cleaned
					}
					writeSSE("tool_result", gin.H{"name": tc.Function.Name, "result": truncateStr(result, 500)})
				}

				messages = append(messages, ollama.Message{
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
		Backend:      "ollama",
	})
	h.emitUsage("ollama", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)

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
		"backend":  "ollama",
		"usage":    gin.H{"prompt_tokens": totalInput, "completion_tokens": totalOutput},
	}
	if len(mediaAttachments) > 0 {
		doneData["attachments"] = mediaAttachments
	}
	writeSSE("done", doneData)
}
