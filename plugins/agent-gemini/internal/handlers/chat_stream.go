package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
)

// ChatStream handles a streaming chat completion request.
// It writes SSE events to the client as tokens arrive.
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
	apiKey := h.apiKey
	model := h.model
	toolLoopLimit := h.toolLoopLimit
	debug := h.debug
	defaultPrompt := h.defaultPrompt
	h.mu.RUnlock()

	if req.Model != "" {
		model = req.Model
	}

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set GEMINI_API_KEY."})
		return
	}

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []gemini.Message{{Role: "user", Content: req.Message}}
	}

	// System prompt.
	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = defaultPrompt
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

	start := time.Now()

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

	// Discover available tools.
	tools := discoverTools(h.sdk)
	var toolDefs []gemini.FunctionDeclaration
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput, totalCached int
	var mediaAttachments []mediaAttachment
	var fullResponse string

	maxIter := toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		var stream <-chan gemini.StreamEvent
		if len(toolDefs) > 0 {
			stream = h.client.StreamChatCompletionWithTools(ctx, model, messages, toolDefs, req.ImageURLs)
		} else {
			stream = h.client.StreamChatCompletion(ctx, model, messages, req.ImageURLs)
		}

		var iterContent string
		var funcCall *gemini.FunctionCall
		var thoughtSig string

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] Gemini error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("gemini stream: %v", ev.Err))
				writeError("Gemini stream error: " + ev.Err.Error())
				return
			}

			if ev.Text != "" {
				iterContent += ev.Text
				writeSSE("token", gin.H{"content": ev.Text})
			}

			if ev.FunctionCall != nil {
				funcCall = ev.FunctionCall
				thoughtSig = ev.ThoughtSignature
			}

			if ev.Usage != nil {
				totalInput += ev.Usage.PromptTokens
				totalOutput += ev.Usage.CompletionTokens
				totalCached += ev.Usage.CachedTokens
			}
		}

		// Handle function call — execute tool and loop.
		if funcCall != nil {
			if maxIter > 0 && iteration == maxIter {
				h.emitEvent("tool_loop", "max iterations reached during streaming")
				fullResponse += iterContent
				break
			}

			h.emitEvent("tool_call", fmt.Sprintf("tool=%s", funcCall.Name))
			writeSSE("tool_call", gin.H{
				"name":      funcCall.Name,
				"arguments": funcCall.Args,
			})

			// Append model's function call to conversation (with thought signature for Gemini round-trip).
			messages = append(messages, gemini.Message{
				FunctionCallName: funcCall.Name,
				FunctionCallArgs: funcCall.Args,
				ThoughtSignature: thoughtSig,
			})

			result, execErr := executeToolCall(h.sdk, tools, funcCall.Name, funcCall.Args)
			if execErr != nil {
				log.Printf("[stream] Tool call %s failed: %v", funcCall.Name, execErr)
				h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", funcCall.Name, execErr))
				result = fmt.Sprintf(`{"error": "%s"}`, execErr.Error())
				writeSSE("tool_result", gin.H{
					"name":  funcCall.Name,
					"error": execErr.Error(),
				})
			} else {
				h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", funcCall.Name, len(result)))
				cleaned, atts := processToolResultMedia(result)
				if len(atts) > 0 {
					mediaAttachments = append(mediaAttachments, atts...)
					result = cleaned
				}
				writeSSE("tool_result", gin.H{
					"name":   funcCall.Name,
					"result": truncateStr(result, 500),
				})
			}

			// Parse result as JSON for function response.
			var resultData map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(result), &resultData); jsonErr != nil {
				resultData = map[string]interface{}{"result": result}
			}

			messages = append(messages, gemini.Message{
				FunctionRespName: funcCall.Name,
				FunctionRespData: resultData,
			})

			// Only send images on the first iteration.
			req.ImageURLs = nil
			continue
		}

		// No function call — final text response.
		fullResponse += iterContent
		break
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:        model,
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
		CachedTokens: totalCached,
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
			CachedTokens: totalCached,
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
		"backend":  "gemini",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
			"cached_tokens":     totalCached,
		},
	}
	if len(mediaAttachments) > 0 {
		doneData["attachments"] = mediaAttachments
	}
	writeSSE("done", doneData)
}
