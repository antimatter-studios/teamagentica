package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/kimi"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/kimicli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-moonshot/internal/usage"
)

// MoonshotAdapter implements agentkit.ProviderAdapter using the Moonshot/Kimi API.
//
// It supports two backends:
//   - API: direct HTTP calls to the Moonshot API with tool loop support
//   - CLI: kimi-cli subprocess (handles tools via MCP internally)
//
// When using the CLI backend, the adapter never returns FinishReasonToolUse —
// the agentkit runtime's tool loop won't trigger because the CLI handles tools.
// When using the API backend, tool calls are returned for agentkit to execute.
type MoonshotAdapter struct {
	mu            sync.RWMutex
	apiKey        string
	model         string
	debug         bool
	defaultPrompt string
	client        *kimi.Client
	kimiCLI       *kimicli.Client
	mcpConfigFile string
	tracker       *usage.Tracker

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	APIKey        string
	Model         string
	Debug         bool
	DefaultPrompt string
	Tracker       *usage.Tracker
}

// NewAdapter creates a MoonshotAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *MoonshotAdapter {
	return &MoonshotAdapter{
		apiKey:        cfg.APIKey,
		model:         cfg.Model,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultPrompt,
		client:        kimi.NewClient(cfg.APIKey, cfg.Debug),
		tracker:       cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *MoonshotAdapter) ProviderName() string { return "moonshot" }

// ModelID implements agentkit.ProviderAdapter.
func (a *MoonshotAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetKimiCLI attaches the Kimi CLI client for the CLI backend.
func (a *MoonshotAdapter) SetKimiCLI(cli *kimicli.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kimiCLI = cli
}

// SetMCPConfigFile sets the path to the MCP config file for the CLI backend.
func (a *MoonshotAdapter) SetMCPConfigFile(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpConfigFile = path
}

// SetEmitEvent sets the event emission callback.
func (a *MoonshotAdapter) SetEmitEvent(fn func(string, string)) {
	a.emitEvent = fn
}

// ApplyConfig updates mutable config fields in-place without restarting.
func (a *MoonshotAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["KIMI_API_KEY"]; ok {
		if v != a.apiKey {
			log.Printf("[config] updating api key")
			a.apiKey = v
			a.client = kimi.NewClient(v, a.debug)
		}
	}
	if v, ok := config["KIMI_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
}

// StreamChat implements agentkit.ProviderAdapter.
//
// If a CLI backend is configured, it delegates to the kimi-cli subprocess.
// Otherwise, it calls the Moonshot API directly with tool support.
func (a *MoonshotAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	kimiCLI := a.kimiCLI
	mcpConfigFile := a.mcpConfigFile
	apiKey := a.apiKey
	debug := a.debug
	a.mu.RUnlock()

	if kimiCLI != nil {
		return a.streamCLI(ctx, req, sink, kimiCLI, mcpConfigFile, debug)
	}

	if apiKey == "" {
		return agentkit.ProviderResult{}, fmt.Errorf("no API key configured — set KIMI_API_KEY")
	}

	return a.streamAPI(ctx, req, sink, debug)
}

// streamAPI handles the direct Moonshot API backend with tool call support.
// It returns tool calls for the agentkit runtime to execute.
func (a *MoonshotAdapter) streamAPI(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, debug bool) (agentkit.ProviderResult, error) {
	model := req.Model

	// Build kimi messages from agentkit messages.
	messages := toKimiMessages(req.Messages)

	// Prepend system prompt.
	if req.SystemPrompt != "" {
		filtered := make([]kimi.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]kimi.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	// Convert agentkit tool definitions to Kimi format.
	var toolDefs []kimi.ToolDef
	for _, td := range req.Tools {
		toolDefs = append(toolDefs, kimi.ToolDef{
			Type: "function",
			Function: kimi.FunctionDef{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		})
	}

	start := time.Now()
	stream := a.client.ChatCompletionStream(ctx, model, messages, toolDefs...)

	var fullResponse string
	var totalInput, totalOutput int
	var toolCalls []kimi.ToolCall
	var finishReason string

	for ev := range stream {
		if ev.Err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("kimi API: %w", ev.Err)
		}

		if ev.Token != "" {
			fullResponse += ev.Token
			sink.SendText(ev.Token)
		}

		// Reasoning content is internal — not forwarded to the sink.

		if len(ev.ToolCalls) > 0 {
			toolCalls = kimi.AccumulateToolCalls(toolCalls, ev.ToolCalls)
		}

		if ev.Usage != nil {
			totalInput += ev.Usage.PromptTokens
			totalOutput += ev.Usage.CompletionTokens
		}

		if ev.FinishReason != "" {
			finishReason = ev.FinishReason
		}
	}

	elapsed := time.Since(start)

	// Record local usage.
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        model,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			DurationMs:   elapsed.Milliseconds(),
		})
	}

	if a.emitEvent != nil {
		if debug {
			a.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms response=%s",
				model, totalInput, totalOutput, elapsed.Milliseconds(), truncate(fullResponse, 200)))
		} else {
			a.emitEvent("chat_response", fmt.Sprintf("model=%s tokens=%d+%d time=%dms len=%d",
				model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
		}
	}

	result := agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}

	// If the model wants tool calls, translate them to agentkit format.
	if finishReason == "tool_calls" && len(toolCalls) > 0 {
		result.FinishReason = agentkit.FinishReasonToolUse
		result.ToolCalls = toAgentkitToolCalls(toolCalls)
	}

	return result, nil
}

// streamCLI handles the CLI subprocess backend.
// The CLI handles its own tool loop via MCP, so we never return FinishReasonToolUse.
func (a *MoonshotAdapter) streamCLI(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, cli *kimicli.Client, mcpConfigFile string, debug bool) (agentkit.ProviderResult, error) {
	model := req.Model

	// Build kimi messages to extract prompt.
	messages := toKimiMessages(req.Messages)

	// Prepend system prompt for prompt building.
	if req.SystemPrompt != "" {
		filtered := make([]kimi.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]kimi.Message{{Role: "system", Content: req.SystemPrompt}}, filtered...)
	}

	prompt := buildPrompt(messages)

	start := time.Now()
	stream := cli.ChatCompletionStream(ctx, model, prompt, mcpConfigFile)

	var fullResponse string
	var totalInput, totalOutput int

	for ev := range stream {
		if ev.Err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("kimi CLI: %w", ev.Err)
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			sink.SendText(ev.Text)
		}

		if ev.Usage != nil {
			totalInput = ev.Usage.InputTokens
			totalOutput = ev.Usage.OutputTokens
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
		})
	}

	if a.emitEvent != nil {
		if debug {
			a.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d time=%dms response=%s",
				model, totalInput, totalOutput, elapsed.Milliseconds(), truncate(fullResponse, 200)))
		} else {
			a.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d time=%dms len=%d",
				model, totalInput, totalOutput, elapsed.Milliseconds(), len(fullResponse)))
		}
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

// toKimiMessages converts agentkit messages to Kimi API format.
func toKimiMessages(msgs []agentkit.Message) []kimi.Message {
	out := make([]kimi.Message, 0, len(msgs))
	for _, m := range msgs {
		km := kimi.Message{
			Role:    m.Role,
			Content: m.Content,
		}

		// Convert tool calls from agentkit to kimi format.
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				km.ToolCalls = append(km.ToolCalls, kimi.ToolCall{
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
			km.Role = "tool"
			km.ToolCallID = m.ToolResult.CallID
			km.Content = m.ToolResult.Content
		}

		out = append(out, km)
	}
	return out
}

// toAgentkitToolCalls converts Kimi tool calls to agentkit format.
func toAgentkitToolCalls(calls []kimi.ToolCall) []agentkit.ToolCall {
	out := make([]agentkit.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = agentkit.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: json.RawMessage(c.Function.Arguments),
		}
	}
	return out
}

// buildPrompt concatenates conversation messages into a single prompt string.
func buildPrompt(messages []kimi.Message) string {
	if len(messages) == 1 {
		return messages[0].Content
	}

	var result string
	for i, msg := range messages {
		if i > 0 {
			result += "\n\n"
		}
		switch msg.Role {
		case "user":
			result += "User: "
		case "assistant":
			result += "Assistant: "
		case "system":
			result += "System: "
		}
		result += msg.Content
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
