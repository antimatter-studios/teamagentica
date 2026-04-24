package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/openrouter"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/usage"
)

// OpenRouterAdapter implements agentkit.ProviderAdapter using the OpenRouter
// API (OpenAI-compatible). The agentkit runtime handles the tool loop —
// this adapter only calls the LLM and streams results through the sink.
type OpenRouterAdapter struct {
	mu      sync.RWMutex
	apiKey  string
	model   string
	debug   bool
	client  *openrouter.Client
	tracker *usage.Tracker
}

// AdapterConfig holds parameters for constructing the adapter.
type AdapterConfig struct {
	APIKey  string
	Model   string
	Debug   bool
	Tracker *usage.Tracker
}

// NewAdapter creates an OpenRouterAdapter.
func NewAdapter(cfg AdapterConfig) *OpenRouterAdapter {
	return &OpenRouterAdapter{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		debug:   cfg.Debug,
		client:  openrouter.NewClient(cfg.APIKey, cfg.Debug),
		tracker: cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *OpenRouterAdapter) ProviderName() string { return "openrouter" }

// ModelID implements agentkit.ProviderAdapter.
func (a *OpenRouterAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// ApplyConfig updates mutable config fields in-place.
func (a *OpenRouterAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["OPENROUTER_API_KEY"]; ok && v != a.apiKey {
		log.Printf("[config] updating API key")
		a.apiKey = v
		a.client = openrouter.NewClient(v, a.debug)
	}
	if v, ok := config["OPENROUTER_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
}

// StreamChat implements agentkit.ProviderAdapter. It calls the OpenRouter
// streaming API and forwards text/tool events through the agentkit EventSink.
func (a *OpenRouterAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	apiKey := a.apiKey
	debug := a.debug
	a.mu.RUnlock()

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("no API key configured — set OPENROUTER_API_KEY")
	}

	model := req.Model
	if model == "" {
		model = a.ModelID()
	}

	// Convert agentkit messages to openrouter format.
	messages := toOpenRouterMessages(req.Messages, req.SystemPrompt)

	// Convert agentkit tool definitions to OpenRouter (OpenAI-compatible) format.
	tools := toOpenRouterTools(req.Tools)

	if debug {
		log.Printf("[openrouter] streaming model=%s messages=%d tools=%d", model, len(messages), len(tools))
	}

	stream := openrouter.ChatCompletionStream(ctx, apiKey, model, messages, tools)

	var totalInput, totalOutput int

	// Accumulate tool call deltas (OpenAI streams tool calls as incremental chunks).
	type toolCallAcc struct {
		ID   string
		Name string
		Args string
	}
	var toolCallAccs []toolCallAcc
	finishReason := agentkit.FinishReasonEndTurn

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[openrouter] stream error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("openrouter: %v", ev.Err)
		}

		if ev.Token != "" {
			sink.SendText(ev.Token)
		}

		// Accumulate tool call deltas.
		for _, tc := range ev.ToolCalls {
			for tc.Index >= len(toolCallAccs) {
				toolCallAccs = append(toolCallAccs, toolCallAcc{})
			}
			if tc.ID != "" {
				toolCallAccs[tc.Index].ID = tc.ID
			}
			if tc.Function.Name != "" {
				toolCallAccs[tc.Index].Name = tc.Function.Name
			}
			toolCallAccs[tc.Index].Args += tc.Function.Arguments
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.PromptTokens
			totalOutput = ev.Usage.CompletionTokens
		}

		if ev.FinishReason == "tool_calls" {
			finishReason = agentkit.FinishReasonToolUse
		} else if ev.FinishReason == "length" {
			finishReason = agentkit.FinishReasonMaxTokens
		} else if ev.FinishReason == "stop" {
			finishReason = agentkit.FinishReasonEndTurn
		}
	}

	// Build final tool calls from accumulated deltas.
	var resultToolCalls []agentkit.ToolCall
	if finishReason == agentkit.FinishReasonToolUse {
		for _, tc := range toolCallAccs {
			if tc.Name == "" {
				continue
			}
			call := agentkit.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: json.RawMessage(tc.Args),
			}
			sink.SendToolCall(call)
			resultToolCalls = append(resultToolCalls, call)
		}
	}

	// Track usage locally.
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
		})
	}

	return agentkit.ProviderResult{
		FinishReason: finishReason,
		ToolCalls:    resultToolCalls,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}, nil
}

// toOpenRouterMessages converts agentkit messages to OpenRouter (OpenAI-compatible) format.
func toOpenRouterMessages(msgs []agentkit.Message, systemPrompt string) []openrouter.Message {
	var out []openrouter.Message

	// Prepend system prompt if provided.
	if systemPrompt != "" {
		out = append(out, openrouter.Message{Role: "system", Content: systemPrompt})
	}

	for _, m := range msgs {
		msg := openrouter.Message{Role: m.Role, Content: m.Content, ImageURLs: m.ImageURLs}

		// Assistant messages with tool calls.
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var toolCalls []openrouter.ToolCallMessage
			for _, tc := range m.ToolCalls {
				toolCalls = append(toolCalls, openrouter.ToolCallMessage{
					ID:   tc.ID,
					Type: "function",
					Function: openrouter.ToolCallFunction{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			msg.ToolCalls = toolCalls
		}

		// Tool result messages.
		if m.Role == "tool" && m.ToolResult != nil {
			msg.ToolCallID = m.ToolResult.CallID
			msg.Content = m.ToolResult.Content
		}

		out = append(out, msg)
	}

	return out
}

// toOpenRouterTools converts agentkit tool definitions to OpenRouter format.
func toOpenRouterTools(defs []agentkit.ToolDefinition) []openrouter.ToolDef {
	if len(defs) == 0 {
		return nil
	}
	tools := make([]openrouter.ToolDef, len(defs))
	for i, d := range defs {
		tools[i] = openrouter.ToolDef{
			Type: "function",
			Function: openrouter.ToolFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		}
	}
	return tools
}
