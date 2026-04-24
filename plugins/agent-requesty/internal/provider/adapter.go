package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-requesty/internal/requesty"
	"github.com/antimatter-studios/teamagentica/plugins/agent-requesty/internal/usage"
)

// RequestyAdapter implements agentkit.ProviderAdapter using the Requesty AI
// router (OpenAI-compatible API). The agentkit runtime handles the tool loop,
// so this adapter maps between agentkit and the OpenAI streaming format.
type RequestyAdapter struct {
	mu      sync.RWMutex
	apiKey  string
	model   string
	debug   bool
	tracker *usage.Tracker

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	APIKey  string
	Model   string
	Debug   bool
	Tracker *usage.Tracker
}

// NewAdapter creates a RequestyAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *RequestyAdapter {
	return &RequestyAdapter{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		debug:   cfg.Debug,
		tracker: cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *RequestyAdapter) ProviderName() string { return "requesty" }

// ModelID implements agentkit.ProviderAdapter.
func (a *RequestyAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetEmitEvent sets the event emission callback.
func (a *RequestyAdapter) SetEmitEvent(fn func(string, string)) {
	a.emitEvent = fn
}

// ApplyConfig updates mutable config fields in-place.
func (a *RequestyAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["REQUESTY_API_KEY"]; ok && v != "" {
		a.apiKey = v
		log.Printf("[config] updated API key")
	}
	if v, ok := config["REQUESTY_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
		log.Printf("[config] debug: %v", a.debug)
	}
}

// StreamChat implements agentkit.ProviderAdapter. It calls the Requesty API
// (OpenAI-compatible) and streams text/tool events through the sink.
func (a *RequestyAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	apiKey := a.apiKey
	debug := a.debug
	a.mu.RUnlock()

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("no API key configured — set REQUESTY_API_KEY")
	}

	model := req.Model
	if model == "" {
		model = a.ModelID()
	}

	// Convert agentkit messages to requesty format.
	messages := toRequestyMessages(req.Messages, req.SystemPrompt)

	// Convert agentkit tool definitions to OpenAI format.
	var tools []requesty.ToolDef
	for _, t := range req.Tools {
		tools = append(tools, requesty.ToolDef{
			Type: "function",
			Function: requesty.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	opts := &requesty.StreamOptions{
		Tools:     tools,
		MaxTokens: req.MaxTokens,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		opts.Temperature = &t
	}

	start := time.Now()
	stream := requesty.ChatCompletionStream(ctx, apiKey, model, messages, opts)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Requesty error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("requesty stream: %v", ev.Err)
		}

		if ev.Token != "" {
			fullResponse += ev.Token
			sink.SendText(ev.Token)
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.PromptTokens
			totalOutput = ev.Usage.CompletionTokens
		}

		// Tool call finish — convert to agentkit format and return.
		if ev.FinishReason == "tool_calls" && len(ev.ToolCalls) > 0 {
			var toolCalls []agentkit.ToolCall
			for _, tc := range ev.ToolCalls {
				toolCalls = append(toolCalls, agentkit.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: json.RawMessage(tc.Function.Arguments),
				})
				sink.SendToolCall(agentkit.ToolCall{
					ID:   tc.ID,
					Name: tc.Function.Name,
				})
			}

			elapsed := time.Since(start)
			a.recordUsage(model, totalInput, totalOutput, elapsed)
			a.emit(debug, model, totalInput, totalOutput, elapsed, fullResponse)

			return agentkit.ProviderResult{
				FinishReason: agentkit.FinishReasonToolUse,
				ToolCalls:    toolCalls,
				Usage: agentkit.Usage{
					InputTokens:  totalInput,
					OutputTokens: totalOutput,
				},
			}, nil
		}
	}

	elapsed := time.Since(start)
	a.recordUsage(model, totalInput, totalOutput, elapsed)
	a.emit(debug, model, totalInput, totalOutput, elapsed, fullResponse)

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}, nil
}

func (a *RequestyAdapter) recordUsage(model string, input, output int, elapsed time.Duration) {
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  input,
			OutputTokens: output,
			TotalTokens:  input + output,
			DurationMs:   elapsed.Milliseconds(),
		})
	}
}

func (a *RequestyAdapter) emit(debug bool, model string, input, output int, elapsed time.Duration, response string) {
	if a.emitEvent == nil {
		return
	}
	if debug {
		a.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
			model, input, output, elapsed.Milliseconds(), truncate(response, 200)))
	} else {
		a.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
			model, input, output, elapsed.Milliseconds(), len(response)))
	}
}

// toRequestyMessages converts agentkit messages to requesty format,
// prepending a system message if a system prompt is provided.
func toRequestyMessages(msgs []agentkit.Message, systemPrompt string) []requesty.Message {
	var out []requesty.Message

	if systemPrompt != "" {
		out = append(out, requesty.Message{Role: "system", Content: systemPrompt})
	}

	for _, m := range msgs {
		msg := requesty.Message{Role: m.Role, Content: m.Content, ImageURLs: m.ImageURLs}

		// Map tool call results (role=tool).
		if m.ToolResult != nil {
			msg.Role = "tool"
			msg.Content = m.ToolResult.Content
			msg.ToolCallID = m.ToolResult.CallID
		}

		// Map assistant tool call requests.
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, requesty.ToolCallDelta{
					ID:   tc.ID,
					Type: "function",
					Function: struct {
						Name      string `json:"name,omitempty"`
						Arguments string `json:"arguments,omitempty"`
					}{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
		}

		out = append(out, msg)
	}

	return out
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
