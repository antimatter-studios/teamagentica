package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/anthropic"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/usage"
)

// claudeDoneEvent extends DoneEvent with Claude-specific fields.
type claudeDoneEvent struct {
	Response  string             `json:"response"`
	Model     string             `json:"model,omitempty"`
	Backend   string             `json:"backend,omitempty"`
	Usage     *pluginsdk.AgentUsage `json:"usage,omitempty"`
	CostUSD   float64            `json:"cost_usd,omitempty"`
	NumTurns  int                `json:"num_turns,omitempty"`
	SessionID string             `json:"session_id,omitempty"`
}

// ChatStream implements pluginsdk.AgentProvider.
func (h *Handler) ChatStream(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		h.mu.RLock()
		model := h.model
		backend := h.backend
		mcpConfig := h.mcpConfig
		debug := h.debug
		h.mu.RUnlock()

		if req.Model != "" {
			model = req.Model
		}

		messages := convertMessages(req)

		switch backend {
		case "cli":
			if h.claudeCLI == nil {
				ch <- pluginsdk.StreamError("CLI backend not initialised")
				return
			}

			systemPrompt := req.SystemPrompt
			if systemPrompt == "" {
				systemPrompt = h.defaultPrompt
			}

			// For persistent CLI processes, send only the latest user message.
			prompt := lastUserMessage(messages)
			if prompt == "" {
				prompt = buildPromptWithSystem(messages, systemPrompt)
			}

			var opts *claudecli.ChatOptions
			if req.WorkspaceID != "" || req.SessionID != "" {
				opts = &claudecli.ChatOptions{
					SessionID: req.SessionID,
				}
				if req.WorkspaceID != "" && isValidWorkspaceID(req.WorkspaceID) {
					opts.WorkspaceDir = h.workspaceDir + "/" + req.WorkspaceID
				}
			}

			maxTurns := 0
			if v, ok := req.Extra["max_turns"]; ok {
				switch t := v.(type) {
				case float64:
					maxTurns = int(t)
				case int:
					maxTurns = t
				}
			}

			start := time.Now()
			stream := h.claudeCLI.ChatCompletionStream(ctx, model, prompt, req.SystemPrompt, maxTurns, nil, mcpConfig, opts)

			var fullResponse string
			var respModel string
			var totalInput, totalOutput, cachedTokens int
			var costUSD float64
			var numTurns int
			var sessionID string

			for ev := range stream {
				if ev.Err != nil {
					log.Printf("[stream] Claude CLI error: %v", ev.Err)
					h.emitEvent("error", fmt.Sprintf("cli stream: %v", ev.Err))
					ch <- pluginsdk.StreamError("Claude stream error: " + ev.Err.Error())
					return
				}

				if ev.Text != "" {
					fullResponse += ev.Text
					ch <- pluginsdk.StreamToken(ev.Text)
				}

				if ev.ToolName != "" {
					ch <- pluginsdk.StreamToolCall(ev.ToolName, "")
				}

				if ev.ToolDone != "" {
					ch <- pluginsdk.StreamToolResult(ev.ToolDone, "")
				}

				if ev.Model != "" {
					respModel = ev.Model
				}

				if ev.Usage != nil {
					totalInput = ev.Usage.InputTokens
					totalOutput = ev.Usage.OutputTokens
					cachedTokens = ev.Usage.CachedTokens
				}

				if ev.CostUSD > 0 {
					costUSD = ev.CostUSD
				}
				if ev.NumTurns > 0 {
					numTurns = ev.NumTurns
				}
				if ev.SessionID != "" {
					sessionID = ev.SessionID
				}
			}

			elapsed := time.Since(start)

			if respModel == "" {
				respModel = model
			}

			h.usage.RecordRequest(usage.RequestRecord{
				Model:        respModel,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				TotalTokens:  totalInput + totalOutput,
				CachedTokens: cachedTokens,
				DurationMs:   elapsed.Milliseconds(),
				Backend:      "cli",
			})
			h.emitUsage("anthropic", respModel, totalInput, totalOutput, totalInput+totalOutput, cachedTokens, elapsed.Milliseconds(), req.UserID)

			if debug {
				h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms response=%s",
					respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
			} else {
				h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms len=%d",
					respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), len(fullResponse)))
			}

			ch <- pluginsdk.AgentStreamEvent{
				Type: "done",
				Data: claudeDoneEvent{
					Response: fullResponse,
					Model:    respModel,
					Backend:  "cli",
					Usage: &pluginsdk.AgentUsage{
						PromptTokens:     totalInput,
						CompletionTokens: totalOutput,
						CachedTokens:     cachedTokens,
					},
					CostUSD:   costUSD,
					NumTurns:  numTurns,
					SessionID: sessionID,
				},
			}

		case "api_key":
			ch <- pluginsdk.StreamError("streaming not yet supported for api_key backend on agent-claude")

		default:
			ch <- pluginsdk.StreamError(fmt.Sprintf("unknown backend %q", backend))
		}
	}()

	return ch
}

// convertMessages converts SDK conversation messages to anthropic messages.
func convertMessages(req pluginsdk.AgentChatRequest) []anthropic.Message {
	if len(req.Conversation) > 0 {
		msgs := make([]anthropic.Message, len(req.Conversation))
		for i, m := range req.Conversation {
			msgs[i] = anthropic.Message{Role: m.Role, Content: m.Content}
		}
		return msgs
	}
	return []anthropic.Message{{Role: "user", Content: req.Message}}
}
