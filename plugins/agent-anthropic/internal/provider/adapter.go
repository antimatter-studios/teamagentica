package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"

	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
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
	tracker      *usage.Tracker
	sdk          *pluginsdk.Client

	// Per-workspace WebSocket connections, keyed by container ID.
	wsConns   map[string]*wsConn
	wsConnsMu sync.Mutex

	// emitEvent is an optional callback for publishing platform events.
	emitEvent func(eventType, detail string)
}

// wsConn holds a WebSocket connection to a workspace exec-server.
type wsConn struct {
	mu       sync.Mutex
	ws       *websocket.Conn
	lastUsed time.Time
}

// AdapterConfig holds all parameters needed to construct the adapter.
type AdapterConfig struct {
	Model         string
	Debug         bool
	DefaultPrompt string
	WorkspaceDir  string
	Tracker       *usage.Tracker
}

// NewAdapter creates an AnthropicAdapter from the given config.
func NewAdapter(cfg AdapterConfig) *AnthropicAdapter {
	return &AnthropicAdapter{
		model:         cfg.Model,
		debug:         cfg.Debug,
		defaultPrompt: cfg.DefaultPrompt,
		workspaceDir:  cfg.WorkspaceDir,
		tracker:       cfg.Tracker,
		wsConns:       make(map[string]*wsConn),
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

// SetSDK sets the plugin SDK client for cross-plugin calls.
func (a *AnthropicAdapter) SetSDK(client *pluginsdk.Client) {
	a.sdk = client
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
	mcpConfig := a.mcpConfig
	debug := a.debug
	a.mu.RUnlock()

	// Per-conversation workspace routing: resolve workspace to container exec URL.
	if req.WorkspaceID != "" && a.sdk != nil {
		containerID, status, err := a.resolveWorkspace(ctx, req.WorkspaceID)
		if err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("workspace resolution failed: %w", err)
		}
		if status != "running" {
			return agentkit.ProviderResult{}, fmt.Errorf("workspace %s is not running (status: %s)", req.WorkspaceID, status)
		}
		wsURL := fmt.Sprintf("ws://teamagentica-mc-%s:9100/exec", containerID)
		return a.streamWorkspace(ctx, req, sink, containerID, wsURL, mcpConfig, debug)
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

	// Attach image URLs. For the local CLI backend we download each URL to a
	// temp file and @-mention the path so Claude CLI loads it as an image
	// attachment (its native mechanism for vision input).
	imageURLs := collectImageURLs(req, messages)
	var tempFiles []string
	prompt, tempFiles = attachLocalImages(prompt, imageURLs)
	defer func() {
		for _, p := range tempFiles {
			_ = os.Remove(p)
		}
	}()

	var opts *claudecli.ChatOptions

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

// resolveWorkspace calls workspace-manager to look up a workspace's container ID and status.
func (a *AnthropicAdapter) resolveWorkspace(ctx context.Context, workspaceID string) (containerID, status string, err error) {
	data, err := a.sdk.RouteToPlugin(ctx, "workspace-manager", "GET", "/workspaces/"+workspaceID, nil)
	if err != nil {
		return "", "", fmt.Errorf("workspace lookup: %w", err)
	}
	var ws struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &ws); err != nil {
		return "", "", fmt.Errorf("decode workspace: %w", err)
	}
	return ws.ID, ws.Status, nil
}

// streamWorkspace handles per-conversation workspace connections via the exec-server
// running inside the workspace container. Each container gets its own pooled WebSocket.
func (a *AnthropicAdapter) streamWorkspace(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, containerID, wsURL, mcpConfig string, debug bool) (agentkit.ProviderResult, error) {
	return a.streamRemoteWith(ctx, req, sink, containerID, wsURL, mcpConfig, debug, "workspace")
}

// streamRemoteWith is the shared implementation for remote exec streaming.
// connKey identifies the connection in the pool (container ID or "legacy").
func (a *AnthropicAdapter) streamRemoteWith(ctx context.Context, req agentkit.ProviderRequest, sink agentkit.EventSink, connKey, wsURL, mcpConfig string, debug bool, backend string) (agentkit.ProviderResult, error) {
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

	// Remote backend: local file paths from this container are invisible inside
	// the workspace, so annotate the prompt with the raw URLs. The CLI running
	// on the other side can fetch via WebFetch or MCP tools.
	imageURLs := collectImageURLs(req, messages)
	prompt = annotateRemoteImages(prompt, imageURLs)

	msg := remoteUserMessage{
		Type:           "message",
		Prompt:         prompt,
		ConversationID: req.SessionID,
	}
	data, _ := json.Marshal(msg)

	// Connect and send, with one retry on dead connection.
	var ws *websocket.Conn
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		ws, err = a.ensureConn(connKey, wsURL, model, systemPrompt, mcpConfig)
		if err != nil {
			return agentkit.ProviderResult{}, fmt.Errorf("%s connect: %w", backend, err)
		}
		if err = ws.WriteMessage(websocket.TextMessage, data); err == nil {
			break
		}
		// Write failed — connection dead, reconnect.
		log.Printf("[%s] write failed, reconnecting to %s", backend, connKey)
		a.resetConn(connKey)
	}
	if err != nil {
		return agentkit.ProviderResult{}, fmt.Errorf("%s write: %w", backend, err)
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
			a.resetConn(connKey)
			return agentkit.ProviderResult{}, fmt.Errorf("%s read: %w", backend, err)
		}

		var ev claudecli.StreamEvent
		if json.Unmarshal(rawMsg, &ev) != nil {
			continue
		}

		if ev.ErrMsg != "" {
			log.Printf("[%s] error: %s", backend, ev.ErrMsg)
			return agentkit.ProviderResult{}, fmt.Errorf("%s: %s", backend, ev.ErrMsg)
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
			Backend:      backend,
		})
	}

	if a.emitEvent != nil {
		if debug {
			a.emitEvent("chat_response", fmt.Sprintf("backend=%s model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d response=%s",
				backend, respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, truncate(fullResponse, 200)))
		} else {
			a.emitEvent("chat_response", fmt.Sprintf("backend=%s model=%s tokens=%d+%d cost=$%.4f time=%dms turns=%d len=%d",
				backend, respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), numTurns, len(fullResponse)))
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

// ensureConn returns (or creates) a WebSocket connection to a workspace exec-server.
// Connections are pooled by key (container ID or "legacy" for static mode).
func (a *AnthropicAdapter) ensureConn(key, wsURL, model, systemPrompt, mcpConfig string) (*websocket.Conn, error) {
	a.wsConnsMu.Lock()
	defer a.wsConnsMu.Unlock()

	if conn, ok := a.wsConns[key]; ok && conn.ws != nil {
		conn.lastUsed = time.Now()
		return conn.ws, nil
	}

	log.Printf("[workspace] connecting to %s (%s)", key, wsURL)
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

	a.wsConns[key] = &wsConn{ws: ws, lastUsed: time.Now()}
	log.Printf("[workspace] connected to %s", key)
	return ws, nil
}

// resetConn closes and removes a pooled WebSocket connection.
func (a *AnthropicAdapter) resetConn(key string) {
	a.wsConnsMu.Lock()
	defer a.wsConnsMu.Unlock()
	if conn, ok := a.wsConns[key]; ok {
		if conn.ws != nil {
			conn.ws.Close()
		}
		delete(a.wsConns, key)
	}
}

// CleanIdleConns closes WebSocket connections idle longer than maxIdle.
func (a *AnthropicAdapter) CleanIdleConns(maxIdle time.Duration) {
	a.wsConnsMu.Lock()
	defer a.wsConnsMu.Unlock()
	now := time.Now()
	for key, conn := range a.wsConns {
		if now.Sub(conn.lastUsed) > maxIdle {
			log.Printf("[workspace] closing idle connection to %s", key)
			if conn.ws != nil {
				conn.ws.Close()
			}
			delete(a.wsConns, key)
		}
	}
}

// --- Message conversion helpers ---

func toAnthropicMessages(msgs []agentkit.Message) []anthropic.Message {
	out := make([]anthropic.Message, len(msgs))
	for i, m := range msgs {
		out[i] = anthropic.Message{
			Role:      m.Role,
			Content:   m.Content,
			ImageURLs: append([]string(nil), m.ImageURLs...),
		}
	}
	return out
}

// collectImageURLs returns the union of top-level request image URLs and
// any ImageURLs attached to user messages. agentkit's convertSDKMessages
// attaches req.ImageURLs onto the last user Message, so in practice these
// overlap — we de-duplicate here so callers can use either entry point.
func collectImageURLs(req agentkit.ProviderRequest, messages []anthropic.Message) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(u string) {
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	for _, u := range req.ImageURLs {
		add(u)
	}
	for _, m := range messages {
		if m.Role != "user" {
			continue
		}
		for _, u := range m.ImageURLs {
			add(u)
		}
	}
	return out
}

// attachLocalImages downloads each URL to a temp file and returns:
//   - the prompt with `@<path>` mentions appended (Claude CLI treats these
//     as local-file attachments and reads them before responding)
//   - the list of temp paths the caller must clean up once the stream ends
//
// Any URL that fails to download is logged and falls through to a bracketed
// URL note so the model still knows an image was attached.
func attachLocalImages(prompt string, urls []string) (string, []string) {
	if len(urls) == 0 {
		return prompt, nil
	}
	var mentions []string
	var notes []string
	var tempFiles []string
	for _, u := range urls {
		path, err := anthropic.DownloadImage(u)
		if err != nil {
			log.Printf("[anthropic] image download failed (%s): %v", u, err)
			notes = append(notes, fmt.Sprintf("[attached image URL: %s]", u))
			continue
		}
		tempFiles = append(tempFiles, path)
		mentions = append(mentions, "@"+path)
	}
	parts := []string{prompt}
	if len(mentions) > 0 {
		parts = append(parts, strings.Join(mentions, " "))
	}
	if len(notes) > 0 {
		parts = append(parts, strings.Join(notes, "\n"))
	}
	return strings.Join(parts, "\n\n"), tempFiles
}

// annotateRemoteImages appends bracketed URL notes to the prompt for remote
// (workspace) backends. Local file paths from the agent container are not
// accessible inside the workspace container, so we hand the model the raw
// URL and let its tools (WebFetch/Read) pull the content.
func annotateRemoteImages(prompt string, urls []string) string {
	if len(urls) == 0 {
		return prompt
	}
	notes := make([]string, 0, len(urls))
	for _, u := range urls {
		notes = append(notes, fmt.Sprintf("[attached image URL: %s]", u))
	}
	return prompt + "\n\n" + strings.Join(notes, "\n")
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
