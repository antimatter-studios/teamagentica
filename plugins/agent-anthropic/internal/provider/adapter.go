package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"

	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/anthropic"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/usage"
)

// AnthropicAdapter implements agentkit.ProviderAdapter using the Claude CLI
// subprocess as the backend. The CLI handles its own tool loop via MCP,
// so StreamChat consumes the full CLI event stream and forwards text/tool
// events through the agentkit EventSink.
//
// Limitations vs. the pre-migration handler:
//   - CostUSD, NumTurns, and SessionID are not included in the agentkit DoneEvent
//     (agentkit's DoneEvent type does not support these fields). They are still
//     tracked locally via the usage tracker and emitted as events.
//   - The agentkit runtime's fullResponse accumulation is currently empty (agentkit
//     bug) — the done event will have an empty response field.
type AnthropicAdapter struct {
	mu           sync.RWMutex
	model        string
	debug        bool
	defaultPrompt string
	claudeCLI    *claudecli.Client
	mcpConfig    string
	workspaceDir string
	execMode     string // "local" or "remote"
	execWSURL    string
	tracker      *usage.Tracker

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	Model         string
	Debug         bool
	DefaultPrompt string
	WorkspaceDir  string
	ExecMode      string
	ExecWSURL     string
	Tracker       *usage.Tracker
}

// NewAdapter creates an AnthropicAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *AnthropicAdapter {
	return &AnthropicAdapter{
		model:         cfg.Model,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultPrompt,
		workspaceDir:  cfg.WorkspaceDir,
		execMode:      cfg.ExecMode,
		execWSURL:     cfg.ExecWSURL,
		tracker:       cfg.Tracker,
	}
}

// ProviderName implements agentkit.ProviderAdapter.
func (a *AnthropicAdapter) ProviderName() string { return "anthropic" }

// ModelID implements agentkit.ProviderAdapter.
func (a *AnthropicAdapter) ModelID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetClaudeCLI attaches the Claude CLI client.
func (a *AnthropicAdapter) SetClaudeCLI(client *claudecli.Client) {
	a.claudeCLI = client
}

// SetMCPConfig sets the path to the MCP config file.
func (a *AnthropicAdapter) SetMCPConfig(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpConfig = path
}

// SetEmitEvent sets the event emission callback.
func (a *AnthropicAdapter) SetEmitEvent(fn func(string, string)) {
	a.emitEvent = fn
}

// CyclePool cycles the CLI process pool.
func (a *AnthropicAdapter) CyclePool() {
	if a.claudeCLI != nil {
		a.claudeCLI.CyclePool()
	}
}

// ApplyConfig updates mutable config fields in-place.
func (a *AnthropicAdapter) ApplyConfig(config map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := config["CLAUDE_MODEL"]; ok && v != "" {
		log.Printf("[config] updating model: %s -> %s", a.model, v)
		a.model = v
	}
	if v, ok := config["PLUGIN_DEBUG"]; ok {
		a.debug = v == "true"
	}
	if v, ok := config["CLAUDE_SKIP_PERMISSIONS"]; ok {
		skip := v == "true"
		if a.claudeCLI != nil {
			a.claudeCLI.SetSkipPermissions(skip)
			log.Printf("[config] skip-permissions: %v", skip)
		}
	}
}

// StreamChat implements agentkit.ProviderAdapter. It delegates to the Claude CLI
// subprocess (or remote exec server) and streams events through the sink.
//
// The CLI handles its own tool loop internally via MCP, so this adapter never
// returns FinishReasonToolUse — the agentkit runtime's tool loop won't trigger.
// The req.Tools field is ignored since tools are discovered by the CLI directly.
func (a *AnthropicAdapter) StreamChat(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink) (agentkit.ProviderResult, error) {
	a.mu.RLock()
	execMode := a.execMode
	execWSURL := a.execWSURL
	mcpConfig := a.mcpConfig
	debug := a.debug
	a.mu.RUnlock()

	if execMode == "remote" && execWSURL != "" {
		return a.streamRemote(ctx, req, sink, execWSURL, mcpConfig, debug)
	}
	return a.streamCLI(ctx, req, sink, mcpConfig, debug)
}

// streamCLI handles the local CLI subprocess backend.
func (a *AnthropicAdapter) streamCLI(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, mcpConfig string, debug bool) (agentkit.ProviderResult, error) {
	if a.claudeCLI == nil {
		return agentkit.ProviderResult{}, fmt.Errorf("CLI backend not initialised")
	}

	model := req.Model

	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = a.renderPrompt(a.defaultPrompt)
	}

	// Convert agentkit messages to anthropic format to extract prompt.
	messages := toAnthropicMessages(req.Messages)
	prompt := lastUserMessage(messages)
	if prompt == "" {
		prompt = buildPromptWithSystem(messages, systemPrompt)
	}

	// Extract extra options from the request context (workspace, session, max_turns).
	// These are passed via message metadata when available.
	var opts *claudecli.ChatOptions
	// Note: workspace/session info was previously extracted from AgentChatRequest
	// fields. In agentkit mode, these aren't directly available in ProviderRequest.
	// The agentkit runtime passes only conversation messages. This is a known
	// limitation — workspace-scoped sessions require a future agentkit extension.

	maxTurns := 0

	start := time.Now()
	stream := a.claudeCLI.ChatCompletionStream(ctx, model, prompt, systemPrompt, maxTurns, nil, mcpConfig, opts)

	var fullResponse string
	var respModel string
	var totalInput, totalOutput, cachedTokens int
	var costUSD float64
	var numTurns int

	for ev := range stream {
		if ev.Err != nil {
			log.Printf("[stream] Claude CLI error: %v", ev.Err)
			return agentkit.ProviderResult{}, fmt.Errorf("CLI stream: %v", ev.Err)
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			sink.SendText(ev.Text)
		}

		if ev.ToolName != "" {
			sink.SendToolCall(agentkit.ToolCall{Name: ev.ToolName})
		}

		// ToolDone events are informational — the CLI handled the tool internally.
		// We emit them as text so the downstream SSE consumer sees tool progress.

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
	}

	elapsed := time.Since(start)
	if respModel == "" {
		respModel = model
	}

	// Track locally.
	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        respModel,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			CachedTokens: cachedTokens,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "cli",
		})
	}

	if a.emitEvent != nil {
		if debug {
			a.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d response=%s",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, truncate(fullResponse, 200)))
		} else {
			a.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d len=%d",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, len(fullResponse)))
		}
	}

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			CachedTokens: cachedTokens,
		},
	}, nil
}

// --- Remote exec backend ---

type remoteProxyConn struct {
	mu   sync.Mutex
	ws   *websocket.Conn
	url  string
	init bool
}

var remoteConn remoteProxyConn

type remoteUserMessage struct {
	Type           string `json:"type"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversation_id"`
}

type remoteInitMessage struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	MCPConfig    string `json:"mcp_config"`
	MaxTurns     int    `json:"max_turns"`
}

// streamRemote handles the remote WebSocket exec server backend.
func (a *AnthropicAdapter) streamRemote(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, execWSURL, mcpConfig string, debug bool) (agentkit.ProviderResult, error) {
	model := req.Model

	systemPrompt := req.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = a.renderPrompt(a.defaultPrompt)
	}

	messages := toAnthropicMessages(req.Messages)
	prompt := lastUserMessage(messages)
	if prompt == "" {
		prompt = buildPromptWithSystem(messages, systemPrompt)
	}

	ws, err := a.ensureRemoteConn(execWSURL, model, systemPrompt, mcpConfig)
	if err != nil {
		return agentkit.ProviderResult{}, fmt.Errorf("remote connect: %w", err)
	}

	msg := remoteUserMessage{
		Type:   "message",
		Prompt: prompt,
	}
	data, _ := json.Marshal(msg)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		a.resetRemoteConn()
		return agentkit.ProviderResult{}, fmt.Errorf("remote write: %w", err)
	}

	start := time.Now()
	var fullResponse string
	var respModel string
	var totalInput, totalOutput, cachedTokens int
	var costUSD float64
	var numTurns int

	for {
		_, rawMsg, err := ws.ReadMessage()
		if err != nil {
			a.resetRemoteConn()
			return agentkit.ProviderResult{}, fmt.Errorf("remote read: %w", err)
		}

		var ev claudecli.StreamEvent
		if json.Unmarshal(rawMsg, &ev) != nil {
			continue
		}

		if ev.ErrMsg != "" {
			log.Printf("[remote] error: %s", ev.ErrMsg)
			return agentkit.ProviderResult{}, fmt.Errorf("remote: %s", ev.ErrMsg)
		}

		if ev.Text != "" {
			fullResponse += ev.Text
			sink.SendText(ev.Text)
		}
		if ev.ToolName != "" {
			sink.SendToolCall(agentkit.ToolCall{Name: ev.ToolName})
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

		if ev.Done {
			break
		}
	}

	elapsed := time.Since(start)
	if respModel == "" {
		respModel = model
	}

	if a.tracker != nil {
		a.tracker.RecordRequest(usage.RequestRecord{
			Model:        respModel,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			CachedTokens: cachedTokens,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "remote",
		})
	}

	if a.emitEvent != nil {
		if debug {
			a.emitEvent("chat_response", fmt.Sprintf("backend=remote model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d response=%s",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, truncate(fullResponse, 200)))
		} else {
			a.emitEvent("chat_response", fmt.Sprintf("backend=remote model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d len=%d",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, len(fullResponse)))
		}
	}

	return agentkit.ProviderResult{
		FinishReason: agentkit.FinishReasonEndTurn,
		Usage: agentkit.Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			CachedTokens: cachedTokens,
		},
	}, nil
}

func (a *AnthropicAdapter) ensureRemoteConn(wsURL, model, systemPrompt, mcpConfig string) (*websocket.Conn, error) {
	remoteConn.mu.Lock()
	defer remoteConn.mu.Unlock()

	if remoteConn.ws != nil && remoteConn.init {
		return remoteConn.ws, nil
	}

	log.Printf("[remote] connecting to %s", wsURL)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}

	init := remoteInitMessage{
		Model:        model,
		SystemPrompt: systemPrompt,
		MCPConfig:    mcpConfig,
	}
	if err := ws.WriteJSON(init); err != nil {
		ws.Close()
		return nil, fmt.Errorf("send init: %w", err)
	}

	var resp map[string]string
	if err := ws.ReadJSON(&resp); err != nil {
		ws.Close()
		return nil, fmt.Errorf("read attached: %w", err)
	}
	if resp["status"] != "attached" {
		ws.Close()
		return nil, fmt.Errorf("unexpected status: %v", resp)
	}

	remoteConn.ws = ws
	remoteConn.url = wsURL
	remoteConn.init = true

	log.Printf("[remote] connected to workspace exec server")
	return ws, nil
}

func (a *AnthropicAdapter) resetRemoteConn() {
	remoteConn.mu.Lock()
	defer remoteConn.mu.Unlock()
	if remoteConn.ws != nil {
		remoteConn.ws.Close()
		remoteConn.ws = nil
		remoteConn.init = false
	}
}

// --- Message conversion helpers ---

func toAnthropicMessages(msgs []agentkit.Message) []anthropic.Message {
	out := make([]anthropic.Message, len(msgs))
	for i, m := range msgs {
		out[i] = anthropic.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

func lastUserMessage(messages []anthropic.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func buildPromptWithSystem(messages []anthropic.Message, systemPrompt string) string {
	if systemPrompt != "" {
		filtered := make([]anthropic.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role != "system" {
				filtered = append(filtered, m)
			}
		}
		messages = append([]anthropic.Message{{Role: "system", Content: systemPrompt}}, filtered...)
	}
	return buildPrompt(messages)
}

func buildPrompt(messages []anthropic.Message) string {
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

// renderPrompt renders a Go template string, stripping template syntax if present.
func (a *AnthropicAdapter) renderPrompt(raw string) string {
	if !strings.Contains(raw, "{{") {
		return raw
	}
	tmpl, err := template.New("prompt").Parse(raw)
	if err != nil {
		log.Printf("[adapter] failed to parse prompt template: %v", err)
		return raw
	}
	data := struct{ Alias string }{}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("[adapter] failed to render prompt template: %v", err)
		return raw
	}
	return buf.String()
}
