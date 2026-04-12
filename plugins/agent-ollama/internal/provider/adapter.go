package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/ollama"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/usage"
)

// OllamaAdapter implements agentkit.ProviderAdapter using Ollama's
// OpenAI-compatible streaming API as the backend.
//
// Unlike the Anthropic adapter (which uses Claude CLI and handles its
// own tool loop), this adapter returns FinishReasonToolUse when the
// model requests tools, letting the agentkit runtime drive the loop.
type OllamaAdapter struct {
	mu       sync.RWMutex
	model    string
	endpoint string
	debug    bool
	tracker  *usage.Tracker
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	Model    string
	Endpoint string
	Debug    bool
	Tracker  *usage.Tracker
}

// NewAdapter creates an OllamaAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *OllamaAdapter {
	return &OllamaAdapter{
		model:    cfg.Model,
		endpoint: cfg.Endpoint,
		debug:    cfg.Debug,
		tracker:  cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *OllamaAdapter) ProviderName() string { return "ollama" }

// ModelID implements agentkit.ProviderAdapter.
func (a *OllamaAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// ApplyConfig updates mutable config fields in-place.
func (a *OllamaAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["OLLAMA_MODEL"]; ok && v != "" {
		log.Printf("[config] adapter updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
}

// StreamChat implements agentkit.ProviderAdapter. It calls the Ollama
// OpenAI-compatible streaming endpoint and forwards events through the sink.
//
// When the model requests tool use, this returns FinishReasonToolUse with
// the accumulated tool calls so the agentkit runtime can execute them.
func (a *OllamaAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	endpoint := a.endpoint
	debug := a.debug
	a.mu.RUnlock()

	model := req.Model
	if model == "" {
		model = a.ModelID()
	}

	// Convert agentkit messages to ollama format.
	messages := toOllamaMessages(req.Messages)

	// Prepend system prompt.
	if req.SystemPrompt != "" {
		filtered := make([]ollama.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]ollama.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	// Convert agentkit tool definitions to Ollama format.
	var toolDefs []ollama.ToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, ollama.ToolDef{
			Type: "function",
			Function: ollama.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	if debug {
		log.Printf("[ollama] StreamChat model=%s messages=%d tools=%d", model, len(messages), len(toolDefs))
	}

	stream := ollama.ChatCompletionStream(ctx, endpoint, model, messages, toolDefs)

	var totalInput, totalOutput int
	var iterToolCalls []ollama.ToolCall

	for ev := range stream {
		if ev.Err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("ollama stream: %w", ev.Err)
		}

		if ev.Token != "" {
			sink.SendText(ev.Token)
		}

		if len(ev.ToolCalls) > 0 {
			iterToolCalls = ollama.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
		}

		if ev.Usage != nil {
			totalInput += ev.Usage.PromptTokens
			totalOutput += ev.Usage.CompletionTokens
		}

		if ev.FinishReason == "tool_calls" {
			break
		}
	}

	resultUsage := agentkit.Usage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
	}

	// If tool calls were requested, convert and return them.
	if len(iterToolCalls) > 0 {
		agentToolCalls := make([]agentkit.ToolCall, len(iterToolCalls))
		for i, tc := range iterToolCalls {
			agentToolCalls[i] = agentkit.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			}
		}

		return agentkit.ProviderResult{
			FinishReason: agentkit.FinishReasonToolUse,
			ToolCalls:    agentToolCalls,
			Usage:        resultUsage,
		}, nil
	}

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage:        resultUsage,
	}, nil
}

// toOllamaMessages converts agentkit messages to ollama format.
func toOllamaMessages(msgs []agentkit.Message) []ollama.Message {
	out := make([]ollama.Message, 0, len(msgs))
	for _, m := range msgs {
		om := ollama.Message{
			Role:    m.Role,
			Content: m.Content,
		}

		// Convert tool calls from agentkit format.
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, ollama.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
		}

		// Convert tool result.
		if m.ToolResult != nil {
			om.ToolCallID = m.ToolResult.CallID
			om.Content = m.ToolResult.Content
		}

		out = append(out, om)
	}
	return out
}
