package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/inception"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/usage"
)

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		h.mu.RLock()
		apiKey := h.apiKey
		model := h.model
		endpoint := h.endpoint
		toolLoopLimit := h.toolLoopLimit
		debug := h.debug
		diffusingCfg := h.diffusing
		instantCfg := h.instant
		h.mu.RUnlock()

		if req.Model != "" {
			model = req.Model
		}

		// Convert SDK conversation messages to inception messages.
		messages := convertMessages(req)

		if apiKey == "" {
			ch <- pluginsdk.StreamError("INCEPTION_API_KEY is not set.")
			return
		}

		// Determine reasoning effort and diffusing from req.Extra.
		var reasoningEffort string
		if v, ok := req.Extra["reasoning_effort"].(string); ok && v != "" {
			reasoningEffort = v
		}
		if reasoningEffort == "" && instantCfg {
			reasoningEffort = "instant"
		}
		diffusing := diffusingCfg
		if v, ok := req.Extra["diffusing"].(bool); ok {
			diffusing = v
		}

		lastMsg := ""
		if len(messages) > 0 {
			lastMsg = messages[len(messages)-1].Content
		}
		if debug {
			h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d stream=true effort=%s diffusing=%v content=%s", model, len(messages), reasoningEffort, diffusing, truncateStr(lastMsg, 200)))
		} else {
			h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d stream=true", model, len(messages)))
		}

		start := time.Now()

		// Discover tools.
		tools := discoverTools(h.sdk)
		var toolDefs []inception.ToolDef
		if len(tools) > 0 {
			toolDefs = buildToolDefs(tools)
			h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
		}

		// System prompt.
		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}
		if systemPrompt != "" {
			filtered := make([]inception.Message, 0, len(messages))
			for _, m := range messages {
				if m.Role != "system" {
					filtered = append(filtered, m)
				}
			}
			messages = append([]inception.Message{{Role: "system", Content: systemPrompt}}, filtered...)
		}

		// Build request options.
		var streamOpts []inception.RequestOption
		if diffusing {
			streamOpts = append(streamOpts, inception.WithDiffusing(true))
		}
		if reasoningEffort != "" {
			streamOpts = append(streamOpts, inception.WithReasoningEffort(reasoningEffort))
		}

		var totalInput, totalOutput int
		var mediaAttachments []pluginsdk.AgentAttachment
		var fullResponse string

		maxIter := toolLoopLimit
		for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
			stream := inception.ChatCompletionStream(ctx, apiKey, endpoint, model, messages, toolDefs, streamOpts...)

			var iterContent string
			var iterToolCalls []inception.ToolCall

			for ev := range stream {
				if ev.Err != nil {
					log.Printf("[stream] Inception error: %v", ev.Err)
					h.emitEvent("error", fmt.Sprintf("inception stream: %v", ev.Err))
					ch <- pluginsdk.StreamError("Inception stream error: " + ev.Err.Error())
					return
				}

				if ev.Token != "" {
					iterContent += ev.Token
					ch <- pluginsdk.StreamToken(ev.Token)
				}

				if len(ev.ToolCalls) > 0 {
					iterToolCalls = inception.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
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

				messages = append(messages, inception.Message{
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

					messages = append(messages, inception.Message{
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
		h.emitUsage("inception", model, totalInput, totalOutput, totalInput+totalOutput, elapsed.Milliseconds(), req.UserID)

		if debug {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
				model, totalInput, totalOutput, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
				model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
		}

		doneEvent := pluginsdk.DoneEvent{
			Response: fullResponse,
			Model:    model,
			Backend:  "inception",
			Usage: &pluginsdk.AgentUsage{
				PromptTokens:     totalInput,
				CompletionTokens: totalOutput,
			},
		}
		if len(mediaAttachments) > 0 {
			doneEvent.Attachments = mediaAttachments
		}
		ch <- pluginsdk.StreamDone(doneEvent)
	}()

	return ch
}

// convertMessages converts SDK conversation messages to inception messages.
func convertMessages(req pluginsdk.AgentChatRequest) []inception.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]inception.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msg := inception.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			}
			// Preserve tool calls from conversation history.
			if m.ToolCalls != nil {
				if tcs, ok := m.ToolCalls.([]inception.ToolCall); ok {
					msg.ToolCalls = tcs
				}
			}
			msgs[i] = msg
		}
		return msgs
	}
	return []inception.Message{{Role: "user", Content: req.Message}}
}
