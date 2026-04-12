package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/usage"
)

// OpenAIAdapter implements agentkit.ProviderAdapter.
//
// It supports two backends:
//   - "api_key": direct OpenAI API calls. The adapter streams one LLM call and
//     returns FinishReasonToolUse when tools are requested, letting agentkit
//     drive the tool loop.
//   - "subscription": Codex CLI subprocess which handles its own tool loop
//     internally. Always returns FinishReasonEndTurn.
type OpenAIAdapter struct {
	mu            sync.RWMutex
	backend       string // "api_key" or "subscription"
	apiKey        string
	model         string
	endpoint      string
	debug         bool
	codexCLI      *codexcli.Client
	tracker       *usage.Tracker

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	Backend  string
	APIKey   string
	Model    string
	Endpoint string
	Debug    bool
	Tracker  *usage.Tracker
}

// NewAdapter creates an OpenAIAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *OpenAIAdapter {
	return &OpenAIAdapter{
		backend:  cfg.Backend,
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		endpoint: cfg.Endpoint,
		debug:    cfg.Debug,
		tracker:  cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *OpenAIAdapter) ProviderName() string { return "openai" }

// ModelID implements agentkit.ProviderAdapter.
func (a *OpenAIAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetCodexCLI attaches the Codex CLI client for the subscription backend.
func (a *OpenAIAdapter) SetCodexCLI(client *codexcli.Client) {
	a.codexCLI = client
}

// SetEmitEvent sets the event emission callback.
func (a *OpenAIAdapter) SetEmitEvent(fn func(string, string)) {
	a.emitEvent = fn
}

// ApplyConfig updates mutable config fields in-place.
func (a *OpenAIAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["OPENAI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["OPENAI_API_KEY"]; ok {
		a.apiKey = v
	}
	if v, ok := config["OPENAI_API_ENDPOINT"]; ok && v != "" {
		log.Printf("[config] updating endpoint: %s -> %s", a.endpoint, v)
		a.endpoint = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
}

// StreamChat implements agentkit.ProviderAdapter.
func (a *OpenAIAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()

	switch backend {
	case "subscription":
		return a.streamSubscription(ctx, req, sink)
	case "api_key":
		return a.streamAPIKey(ctx, req, sink)
	default:
		return agentkit.ProviderResult{}, fmt.Errorf("unknown backend %q", backend)
	}
}

// streamSubscription handles streaming via the Codex CLI backend.
// The CLI handles its own tool loop, so we always return FinishReasonEndTurn.
func (a *OpenAIAdapter) streamSubscription(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	if a.codexCLI == nil || !a.codexCLI.IsAuthenticated() {
		return agentkit.ProviderResult{}, fmt.Errorf("subscription backend is not authenticated")
	}

	a.mu.RLock()
	debug := a.debug
	a.mu.RUnlock()

	model := req.Model

	messages := toOpenAIMessages(req.Messages)

	// Inject system prompt.
	if req.SystemPrompt != "" {
		filtered := make([]openai.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]openai.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	workdir := ""
	if req.WorkspaceID != "" {
		workdir = os.Getenv("CODEX_DATA_PATH")
		if workdir == "" {
			workdir = "/data"
		}
		workdir = workdir + "/workspaces/" + req.WorkspaceID
	}

	stream := a.codexCLI.ChatCompletionStream(ctx, model, messages, nil, workdir, req.SessionID)

	start := time.Now()
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Codex CLI error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("codex stream: %v", ev.Err)
		}

		if ev.Text != "" {
			sink.SendText(ev.Text)
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.PromptTokens
			totalOutput = ev.Usage.CompletionTokens
		}
	}

	elapsed := time.Since(start)

	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "subscription",
		})
	}

	if a.emitEvent != nil {
		a.emitEvent("chat_response", fmt.Sprintf("backend=subscription model=%s tokens=%d+%d time=%dms",
			model, totalInput, totalOutput, elapsed.Milliseconds()))
	}

	_ = debug

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}, nil
}

// streamAPIKey handles streaming via the OpenAI API directly.
// Returns FinishReasonToolUse when the model requests tool calls, letting
// agentkit drive the tool loop.
func (a *OpenAIAdapter) streamAPIKey(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	apiKey := a.apiKey
	endpoint := a.endpoint
	debug := a.debug
	a.mu.RUnlock()

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("api_key backend is configured but OPENAI_API_KEY is not set")
	}

	model := req.Model

	// Convert agentkit messages to OpenAI format.
	messages := toOpenAIMessages(req.Messages)

	// Inject system prompt.
	if req.SystemPrompt != "" {
		filtered := make([]openai.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]openai.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	// Convert agentkit tool definitions to OpenAI format.
	var toolDefs []openai.ToolDef
	for _, t := range req.Tools {
		toolDefs = append(toolDefs, openai.ToolDef{
			Type: "function",
			Function: openai.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	stream := openai.ChatCompletionStream(ctx, apiKey, endpoint, model, messages, toolDefs)

	start := time.Now()
	var totalInput, totalOutput int
	var iterToolCalls []openai.ToolCall

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] OpenAI error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("openai stream: %v", ev.Err)
		}

		if ev.Token != "" {
			sink.SendText(ev.Token)
		}

		if len(ev.ToolCalls) > 0 {
			iterToolCalls = openai.AccumulateToolCalls(iterToolCalls, ev.ToolCalls)
		}

		if ev.Usage != nil {
			totalInput += ev.Usage.PromptTokens
			totalOutput += ev.Usage.CompletionTokens
		}
	}

	elapsed := time.Since(start)

	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "api_key",
		})
	}

	if a.emitEvent != nil {
		a.emitEvent("chat_response", fmt.Sprintf("backend=api_key model=%s tokens=%d+%d time=%dms",
			model, totalInput, totalOutput, elapsed.Milliseconds()))
	}

	_ = debug

	// If the model requested tool calls, convert and return FinishReasonToolUse.
	if len(iterToolCalls) > 0 {
		agentkitCalls := make([]agentkit.ToolCall, len(iterToolCalls))
		for i, tc := range iterToolCalls {
			agentkitCalls[i] = agentkit.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			}
		}
		return agentkit.ProviderResult{
			FinishReason: agentkit.FinishReasonToolUse,
			ToolCalls:    agentkitCalls,
			Usage: agentkit.Usage{
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
			},
		}, nil
	}

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}, nil
}

// --- Message conversion helpers ---

// toOpenAIMessages converts agentkit Messages to OpenAI API format.
func toOpenAIMessages(msgs []agentkit.Message) []openai.Message {
	out := make([]openai.Message, 0, len(msgs))
	for _, m := range msgs {
		msg := openai.Message{
			Role:    m.Role,
			Content: m.Content,
		}

		// Convert tool calls from agentkit format.
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
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

		out = append(out, msg)
	}
	return out
}
