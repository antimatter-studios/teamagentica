package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/inception"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/usage"
)

// InceptionAdapter implements agentkit.ProviderAdapter using the Inception
// (Mercury) API as the backend. The agentkit runtime drives the tool loop,
// so this adapter handles a single LLM call per StreamChat invocation.
type InceptionAdapter struct {
	mu        sync.RWMutex
	apiKey    string
	model     string
	endpoint  string
	diffusing bool
	instant   bool
	debug     bool
	tracker   *usage.Tracker
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	APIKey    string
	Model     string
	Endpoint  string
	Diffusing bool
	Instant   bool
	Debug     bool
	Tracker   *usage.Tracker
}

// NewAdapter creates an InceptionAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *InceptionAdapter {
	return &InceptionAdapter{
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		endpoint:  cfg.Endpoint,
		diffusing: cfg.Diffusing,
		instant:   cfg.Instant,
		debug:     cfg.Debug,
		tracker:   cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *InceptionAdapter) ProviderName() string { return "inception" }

// ModelID implements agentkit.ProviderAdapter.
func (a *InceptionAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// ApplyConfig updates mutable config fields in-place.
func (a *InceptionAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["INCEPTION_API_KEY"]; ok {
		a.apiKey = v
	}
	if v, ok := config["INCEPTION_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["INCEPTION_API_ENDPOINT"]; ok && v != "" {
		log.Printf("[config] updating endpoint: %s -> %s", a.endpoint, v)
		a.endpoint = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
	if v, ok := config["INCEPTION_DIFFUSING"]; ok {
		a.diffusing = v == "true"
	}
	if v, ok := config["INCEPTION_INSTANT"]; ok {
		a.instant = v == "true"
	}
}

// StreamChat implements agentkit.ProviderAdapter. It calls the Inception
// streaming API and forwards events through the sink. When the LLM requests
// tool use, it returns FinishReasonToolUse so the agentkit runtime can
// execute tools and call StreamChat again with the results appended.
func (a *InceptionAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	apiKey := a.apiKey
	endpoint := a.endpoint
	diffusing := a.diffusing
	instant := a.instant
	debug := a.debug
	a.mu.RUnlock()

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("INCEPTION_API_KEY is not set")
	}

	model := req.Model
	if model == "" {
		model = a.ModelID()
	}

	// Convert agentkit messages to inception messages.
	messages := toInceptionMessages(req)

	// Convert agentkit tool definitions to inception format.
	var toolDefs []inception.ToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, inception.ToolDef{
			Type: "function",
			Function: inception.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Build request options.
	var streamOpts []inception.RequestOption
	if diffusing {
		streamOpts = append(streamOpts, inception.WithDiffusing(true))
	}
	if instant {
		streamOpts = append(streamOpts, inception.WithReasoningEffort("instant"))
	}

	if debug {
		log.Printf("[inception] StreamChat model=%s messages=%d tools=%d", model, len(messages), len(toolDefs))
	}

	stream := inception.ChatCompletionStream(ctx, apiKey, endpoint, model, messages, toolDefs, streamOpts...)

	var totalInput, totalOutput int
	var iterToolCalls []inception.ToolCall
	var finishReason string

	for ev := range stream {
		if ev.Err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("inception stream: %v", ev.Err)
		}

		if ev.Token != "" {
			sink.SendText(ev.Token)
		}

		if len(ev.ToolCalls) > 0 {
			iterToolCalls = inception.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.PromptTokens
			totalOutput = ev.Usage.CompletionTokens
		}

		if ev.FinishReason != "" {
			finishReason = ev.FinishReason
		}
	}

	// Track locally.
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
		})
	}

	result := agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}

	// If the LLM requested tool calls, convert them to agentkit format
	// and return FinishReasonToolUse so the runtime drives the tool loop.
	if finishReason == "tool_calls" && len(iterToolCalls) > 0 {
		result.FinishReason = agentkit.FinishReasonToolUse
		for _, tc := range iterToolCalls {
			result.ToolCalls = append(result.ToolCalls, agentkit.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	return result, nil
}

// toInceptionMessages converts agentkit ProviderRequest messages to inception format,
// prepending the system prompt as the first message.
func toInceptionMessages(req agentkit.ProviderRequest) []inception.Message {
	var messages []inception.Message

	// Prepend system prompt.
	if req.SystemPrompt != "" {
		messages = append(messages, inception.Message{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msg := inception.Message{
			Role:      m.Role,
			Content:   m.Content,
			ImageURLs: m.ImageURLs,
		}

		// Skip system messages from conversation (we already prepended ours).
		if m.Role == "system" {
			continue
		}

		// Convert tool calls from agentkit format to inception format.
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, inception.ToolCall{
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
			msg.Role = "tool"
			msg.ToolCallID = m.ToolResult.CallID
			msg.Content = m.ToolResult.Content
		}

		messages = append(messages, msg)
	}

	return messages
}
