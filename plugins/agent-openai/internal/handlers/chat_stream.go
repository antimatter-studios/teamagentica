package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/usage"
)

// ChatStream handles a streaming chat completion request.
// It writes SSE events to the client as tokens arrive.
// Supports both api_key (OpenAI direct) and subscription (Codex CLI) backends.
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
	backend := h.backend
	apiKey := h.apiKey
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
		messages = []openai.Message{{Role: "user", Content: req.Message}}
	}

	// System prompt.
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

	switch backend {
	case "subscription":
		h.chatStreamSubscription(ctx, c, model, messages, req.ImageURLs, req.WorkspaceID, debug, userID, start, writeSSE, writeError)
	case "api_key":
		if apiKey == "" {
			writeError("api_key backend is configured but OPENAI_API_KEY is not set")
			return
		}
		h.chatStreamAPIKey(ctx, c, apiKey, endpoint, model, messages, toolLoopLimit, debug, userID, start, writeSSE, writeError)
	default:
		writeError(fmt.Sprintf("unknown backend %q", backend))
	}
}

// chatStreamSubscription handles streaming via the Codex CLI backend.
func (h *Handler) chatStreamSubscription(ctx context.Context, c *gin.Context, model string, messages []openai.Message, imageURLs []string, workspaceID string, debug bool, userID string, start time.Time, writeSSE func(string, interface{}), writeError func(string)) {
	if h.codexCLI == nil || !h.codexCLI.IsAuthenticated() {
		writeError("subscription backend is not authenticated")
		return
	}

	workdir := ""
	if workspaceID != "" && isValidWorkspaceID(workspaceID) {
		workdir = os.Getenv("CODEX_DATA_PATH")
		if workdir == "" {
			workdir = "/data"
		}
		workdir = workdir + "/workspaces/" + workspaceID
	}

	stream := h.codexCLI.ChatCompletionStream(ctx, model, messages, imageURLs, workdir)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Codex CLI error: %v", ev.Err)
			h.emitEvent("error", fmt.Sprintf("subscription stream: %v", ev.Err))
			writeError("Codex stream error: " + ev.Err.Error())
			return
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			writeSSE("token", gin.H{"content": ev.Text})
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.PromptTokens
			totalOutput = ev.Usage.CompletionTokens
		}
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:        model,
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
		DurationMs:   elapsed.Milliseconds(),
		Backend:      "subscription",
	})
	h.emitUsage("openai", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)

	if debug {
		h.emitEvent("chat_response", fmt.Sprintf("backend=subscription model=%s tokens=%d+%d time=%dms response=%s",
			model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
	} else {
		h.emitEvent("chat_response", fmt.Sprintf("backend=subscription model=%s tokens=%d+%d time=%dms len=%d",
			model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
	}

	writeSSE("done", gin.H{
		"response": fullResponse,
		"model":    model,
		"backend":  "subscription",
		"usage": gin.H{
			"prompt_tokens":     totalInput,
			"completion_tokens": totalOutput,
		},
	})
}

// chatStreamAPIKey handles streaming via the OpenAI API directly.
func (h *Handler) chatStreamAPIKey(ctx context.Context, c *gin.Context, apiKey, endpoint, model string, messages []openai.Message, toolLoopLimit int, debug bool, userID string, start time.Time, writeSSE func(string, interface{}), writeError func(string)) {
	// Discover tools.
	tools := discoverTools(h.sdk)
	var toolDefs []openai.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput int
	var mediaAttachments []mediaAttachment
	var fullResponse string

	maxIter := toolLoopLimit
	for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
		stream := openai.ChatCompletionStream(ctx, apiKey, endpoint, model, messages, toolDefs)

		var iterContent string
		var iterToolCalls []openai.ToolCall

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] OpenAI error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("openai stream: %v", ev.Err))
				writeError("OpenAI stream error: " + ev.Err.Error())
				return
			}

			if ev.Token != "" {
				iterContent += ev.Token
				writeSSE("token", gin.H{"content": ev.Token})
			}

			if len(ev.ToolCalls) > 0 {
				iterToolCalls = openai.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
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

			messages = append(messages, openai.Message{
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

				messages = append(messages, openai.Message{
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
		Backend:      "api_key",
	})
	h.emitUsage("openai", model, totalInput, totalOutput, totalInput+totalOutput, 0, elapsed.Milliseconds(), userID)

	if debug {
		h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms response=%s",
			model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
	} else {
		h.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms len=%d",
			model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
	}

	doneData := gin.H{
		"response": fullResponse,
		"model":    model,
		"backend":  "api_key",
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
