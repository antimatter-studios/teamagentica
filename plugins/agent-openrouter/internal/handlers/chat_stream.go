package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/openrouter"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/usage"
)

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		h.mu.RLock()
		apiKey := h.apiKey
		model := h.model
		debug := h.debug
		h.mu.RUnlock()

		if req.Model != "" {
			model = req.Model
		}

		messages := convertMessages(req)

		if apiKey == "" {
			ch <- pluginsdk.StreamError("No API key configured. Set OPENROUTER_API_KEY.")
			return
		}

		lastMsg := ""
		if len(messages) > 0 {
			lastMsg = messages[len(messages)-1].Content
		}
		if debug {
			h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d content=%s", model, len(messages), truncateStr(lastMsg, 200)))
		} else {
			h.emitEvent("chat_request", fmt.Sprintf("model=%s messages=%d", model, len(messages)))
		}

		start := time.Now()

		stream := openrouter.ChatCompletionStream(ctx, apiKey, model, messages)

		var fullResponse string
		var totalInput, totalOutput int

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] OpenRouter error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("openrouter stream: %v", ev.Err))
				ch <- pluginsdk.StreamError("OpenRouter stream error: " + ev.Err.Error())
				return
			}

			if ev.Token != "" {
				fullResponse += ev.Token
				ch <- pluginsdk.StreamToken(ev.Token)
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
		})
		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:       req.UserID,
				Provider:     "openrouter",
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

		ch <- pluginsdk.StreamDone(pluginsdk.DoneEvent{
			Response: fullResponse,
			Model:    model,
			Backend:  "openrouter",
			Usage: &pluginsdk.AgentUsage{
				PromptTokens:     totalInput,
				CompletionTokens: totalOutput,
			},
		})
	}()

	return ch
}

// convertMessages converts SDK conversation messages to openrouter messages.
func convertMessages(req pluginsdk.AgentChatRequest) []openrouter.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]openrouter.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msgs[i] = openrouter.Message{Role: m.Role, Content: m.Content}
		}
		return msgs
	}
	return []openrouter.Message{{Role: "user", Content: req.Message}}
}
