package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
)

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

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
			ch <- pluginsdk.StreamError("No API key configured. Set GEMINI_API_KEY.")
			return
		}

		messages := convertMessages(req)
		if len(messages) == 0 {
			ch <- pluginsdk.StreamError("message or conversation required")
			return
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

		// Discover available tools.
		tools := discoverTools(h.sdk)
		var toolDefs []gemini.FunctionDeclaration
		if len(tools) > 0 {
			toolDefs = buildToolDefs(tools)
			h.emitEvent("tool_discovery", fmt.Sprintf("found %d tools", len(tools)))
		}

		var totalInput, totalOutput, totalCached int
		var mediaAttachments []pluginsdk.AgentAttachment
		var fullResponse string

		imageURLs := req.ImageURLs

		maxIter := toolLoopLimit
		for iteration := 0; maxIter == 0 || iteration <= maxIter; iteration++ {
			var stream <-chan gemini.StreamEvent
			if len(toolDefs) > 0 {
				stream = h.client.StreamChatCompletionWithTools(ctx, model, messages, toolDefs, imageURLs)
			} else {
				stream = h.client.StreamChatCompletion(ctx, model, messages, imageURLs)
			}

			var iterContent string
			var funcCall *gemini.FunctionCall
			var thoughtSig string

			for ev := range stream {
				if ev.Err != nil {
					log.Printf("[stream] Gemini error: %v", ev.Err)
					h.emitEvent("error", fmt.Sprintf("gemini stream: %v", ev.Err))
					ch <- pluginsdk.StreamError("Gemini stream error: " + ev.Err.Error())
					return
				}

				if ev.Text != "" {
					iterContent += ev.Text
					ch <- pluginsdk.StreamToken(ev.Text)
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

				argsJSON, _ := json.Marshal(funcCall.Args)
				h.emitEvent("tool_call", fmt.Sprintf("tool=%s", funcCall.Name))
				ch <- pluginsdk.StreamToolCall(funcCall.Name, string(argsJSON))

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
					ch <- pluginsdk.StreamToolError(funcCall.Name, execErr.Error())
				} else {
					h.emitEvent("tool_result", fmt.Sprintf("tool=%s result_len=%d", funcCall.Name, len(result)))
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
					ch <- pluginsdk.StreamToolResult(funcCall.Name, truncateStr(result, 500))
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
				imageURLs = nil
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
				UserID:       req.UserID,
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

		doneEvent := pluginsdk.DoneEvent{
			Response: fullResponse,
			Model:    model,
			Backend:  "gemini",
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

// convertMessages converts SDK conversation messages to gemini messages.
func convertMessages(req pluginsdk.AgentChatRequest) []gemini.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]gemini.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msgs[i] = gemini.Message{
				Role:    m.Role,
				Content: m.Content,
			}
		}
		return msgs
	}
	if req.Message == "" {
		return nil
	}
	return []gemini.Message{{Role: "user", Content: req.Message}}
}
