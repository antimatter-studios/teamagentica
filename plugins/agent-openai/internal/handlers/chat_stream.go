package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/usage"
)

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

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

		messages := convertMessages(req)

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

		start := time.Now()

		switch backend {
		case "subscription":
			h.chatStreamSubscription(ctx, ch, model, messages, req.ImageURLs, req.WorkspaceID, req.SessionID, debug, req.UserID, start)
		case "api_key":
			if apiKey == "" {
				ch <- pluginsdk.StreamError("api_key backend is configured but OPENAI_API_KEY is not set")
				return
			}
			h.chatStreamAPIKey(ctx, ch, apiKey, endpoint, model, messages, toolLoopLimit, debug, req.UserID, start)
		default:
			ch <- pluginsdk.StreamError(fmt.Sprintf("unknown backend %q", backend))
		}
	}()

	return ch
}

// chatStreamSubscription handles streaming via the Codex CLI backend.
func (h *Handler) chatStreamSubscription(ctx context.Context, ch chan<- pluginsdk.AgentStreamEvent, model string, messages []openai.Message, imageURLs []string, workspaceID string, sessionID string, debug bool, userID string, start time.Time) {
	if h.codexCLI == nil || !h.codexCLI.IsAuthenticated() {
		ch <- pluginsdk.StreamError("subscription backend is not authenticated")
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

	stream := h.codexCLI.ChatCompletionStream(ctx, model, messages, imageURLs, workdir, sessionID)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Codex CLI error: %v", ev.Err)
			h.emitEvent("error", fmt.Sprintf("subscription stream: %v", ev.Err))
			ch <- pluginsdk.StreamError("Codex stream error: " + ev.Err.Error())
			return
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			ch <- pluginsdk.StreamToken(ev.Text)
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

	ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
		Response: fullResponse,
		Model:    model,
		Backend:  "subscription",
		Usage: &pluginsdk.AgentUsage{
			PromptTokens:     totalInput,
			CompletionTokens: totalOutput,
		},
	})
}

// chatStreamAPIKey handles streaming via the OpenAI API directly.
func (h *Handler) chatStreamAPIKey(ctx context.Context, ch chan<- pluginsdk.AgentStreamEvent, apiKey, endpoint, model string, messages []openai.Message, toolLoopLimit int, debug bool, userID string, start time.Time) {
	// Discover tools.
	tools := discoverTools(h.sdk)
	var toolDefs []openai.ToolDef
	if len(tools) > 0 {
		toolDefs = buildToolDefs(tools)
		h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
	}

	var totalInput, totalOutput int
	var mediaAttachments []pluginsdk.AgentAttachment
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
				ch <- pluginsdk.StreamError("OpenAI stream error: " + ev.Err.Error())
				return
			}

			if ev.Token != "" {
				iterContent += ev.Token
				ch <- pluginsdk.StreamToken(ev.Token)
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
				ch <- pluginsdk.StreamToolCall(tc.Function.Name, tc.Function.Arguments)

				result, err := executeToolCall(h.sdk, tools, tc)
				if err != nil {
					log.Printf("[stream] Tool call %s failed: %v", tc.Function.Name, err)
					h.emitEvent("tool_error", fmt.Sprintf("tool=%s error=%v", tc.Function.Name, err))
					result = fmt.Sprintf(`{"error": "%s"}`, err.Error())
					ch <- pluginsdk.StreamToolError(tc.Function.Name, err.Error())
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", tc.Function.Name, len(result)))
					cleaned, atts := processToolResultMedia(result)
					if len(atts) > 0 {
						for _, a := range atts {
							mediaAttachments = append(mediaAttachments, pluginsdk.AgentAttachment{
								MimeType:  a.MimeType,
								ImageData: a.ImageData,
								Type:      a.Type,
								URL:       a.URL,
								Filename:  a.Filename,
							})
						}
						result = cleaned
					}
					ch <- pluginsdk.StreamToolResult(tc.Function.Name, truncateStr(result, 500))
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

	doneEvent := pluginsdk.DoneEvent{
		Response: fullResponse,
		Model:    model,
		Backend:  "api_key",
		Usage: &pluginsdk.AgentUsage{
			PromptTokens:     totalInput,
			CompletionTokens: totalOutput,
		},
	}
	if len(mediaAttachments) > 0 {
		doneEvent.Attachments = mediaAttachments
	}
	ch <- pluginsdk.StreamDone(doneEvent)
}

// convertMessages converts SDK conversation messages to openai messages.
func convertMessages(req pluginsdk.AgentChatRequest) []openai.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]openai.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msg := openai.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			}
			if m.ToolCalls != nil {
				if tcs, ok := m.ToolCalls.([]openai.ToolCall); ok {
					msg.ToolCalls = tcs
				}
			}
			msgs[i] = msg
		}
		return msgs
	}
	return []openai.Message{{Role: "user", Content: req.Message}}
}
