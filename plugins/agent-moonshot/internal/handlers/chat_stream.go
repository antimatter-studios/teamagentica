package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/kimi"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/kimicli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/usage"
)

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		h.mu.RLock()
		kimiCLI := h.kimiCLI
		mcpConfigFile := h.mcpConfigFile
		apiKey := h.apiKey
		model := h.model
		toolLoopLimit := h.toolLoopLimit
		debug := h.debug
		h.mu.RUnlock()

		if kimiCLI == nil && apiKey == "" {
			ch <- pluginsdk.StreamError("No API key configured. Set KIMI_API_KEY.")
			return
		}

		if req.Model != "" {
			model = req.Model
		}

		messages := convertMessages(req)
		if len(messages) == 0 {
			ch <- pluginsdk.StreamError("message or conversation required")
			return
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

		start := time.Now()

		// CLI backend: delegate to kimi-cli subprocess (handles tools via MCP).
		if kimiCLI != nil {
			h.chatStreamCLI(ctx, ch, kimiCLI, mcpConfigFile, model, messages, debug, req.UserID, start)
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
		var mediaAttachments []pluginsdk.AgentAttachment
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
					ch <- pluginsdk.StreamError("Kimi stream error: " + ev.Err.Error())
					return
				}

				if ev.Token != "" {
					iterContent += ev.Token
					ch <- pluginsdk.StreamToken(ev.Token)
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
							mediaAttachments = append(mediaAttachments, atts...)
							result = cleaned
						}
						ch <- pluginsdk.StreamToolResult(tc.Function.Name, truncateStr(result, 500))
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
				UserID:       req.UserID,
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

		done := pluginsdk.DoneEvent{
			Response: fullResponse,
			Model:    model,
			Backend:  "kimi",
			Usage: &pluginsdk.AgentUsage{
				PromptTokens:     totalInput,
				CompletionTokens: totalOutput,
			},
		}
		if len(mediaAttachments) > 0 {
			done.Attachments = mediaAttachments
		}
		ch <- pluginsdk.StreamDone(done)
	}()

	return ch
}

// chatStreamCLI handles streaming via the Kimi CLI subprocess.
func (h *Handler) chatStreamCLI(ctx context.Context, ch chan<- pluginsdk.AgentStreamEvent, cli *kimicli.Client, mcpConfigFile, model string, messages []kimi.Message, debug bool, userID string, start time.Time) {
	prompt := buildPrompt(messages)

	stream := cli.ChatCompletionStream(ctx, model, prompt, mcpConfigFile)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Kimi CLI error: %v", ev.Err)
			h.emitEvent("error", fmt.Sprintf("kimi-cli stream: %v", ev.Err))
			ch <- pluginsdk.StreamError("Kimi CLI error: " + ev.Err.Error())
			return
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			ch <- pluginsdk.StreamToken(ev.Text)
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

	ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
		Response: fullResponse,
		Model:    model,
		Backend:  "cli",
		Usage: &pluginsdk.AgentUsage{
			PromptTokens:     totalInput,
			CompletionTokens: totalOutput,
		},
	})
}

// convertMessages converts SDK conversation messages to kimi messages.
func convertMessages(req pluginsdk.AgentChatRequest) []kimi.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]kimi.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msgs[i] = kimi.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		}
		return msgs
	}
	if req.Message != "" {
		return []kimi.Message{{Role: "user", Content: req.Message}}
	}
	return nil
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
