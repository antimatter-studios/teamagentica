package claudecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Client wraps the Claude Code CLI binary for chat completions.
type Client struct {
	binary          string
	workdir         string
	claudeDir       string // CLAUDE_CONFIG_DIR equivalent
	timeout         time.Duration
	debug           bool
	skipPermissions bool
	tlsCert         string // mTLS client cert for MCP server connections.
	tlsKey          string
	tlsCA           string
	poolMax         int           // configurable max hot pool size (default 10)
	poolTTL         time.Duration // configurable idle TTL (0 = use default)

	// Process pool for persistent CLI subprocesses.
	pool   *Pool
	poolMu sync.Mutex
}

// NewClient creates a new Claude CLI client.
func NewClient(binary, workdir, claudeDir string, timeoutSec int, debug bool) *Client {
	return &Client{
		binary:    binary,
		workdir:   workdir,
		claudeDir: claudeDir,
		timeout:   time.Duration(timeoutSec) * time.Second,
		debug:     debug,
	}
}

// SetSkipPermissions enables --dangerously-skip-permissions on CLI invocations.
func (c *Client) SetSkipPermissions(skip bool) {
	c.skipPermissions = skip
}

// SetPoolMax configures the maximum hot pool size (minimum 2).
func (c *Client) SetPoolMax(n int) {
	c.poolMax = n
}

// SetPoolTTL configures the idle TTL in seconds for pooled processes.
func (c *Client) SetPoolTTL(seconds int) {
	if seconds > 0 {
		c.poolTTL = time.Duration(seconds) * time.Second
	}
}

// SetTLS configures mTLS client certs for the Claude CLI subprocess.
func (c *Client) SetTLS(cert, key, ca string) {
	c.tlsCert = cert
	c.tlsKey = key
	c.tlsCA = ca
}

func (c *Client) env() []string {
	env := os.Environ()
	env = append(env, "CLAUDE_CONFIG_DIR="+c.claudeDir)
	// Disable interactive prompts.
	env = append(env, "CI=1")
	// mTLS for MCP server connections.
	if c.tlsCert != "" {
		env = append(env, "CLAUDE_CODE_CLIENT_CERT="+c.tlsCert)
		env = append(env, "CLAUDE_CODE_CLIENT_KEY="+c.tlsKey)
	}
	if c.tlsCA != "" {
		env = append(env, "NODE_EXTRA_CA_CERTS="+c.tlsCA)
	}
	return env
}

// IsAvailable checks if the Claude CLI binary exists and is runnable.
func (c *Client) IsAvailable() bool {
	cmd := exec.Command(c.binary, "--version")
	cmd.Env = c.env()
	return cmd.Run() == nil
}

// Shutdown terminates all persistent subprocesses.
func (c *Client) Shutdown() {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()
	if c.pool != nil {
		c.pool.Shutdown()
		c.pool = nil
	}
}

// ensurePool lazily creates the process pool on first use.
func (c *Client) ensurePool(cfg processConfig) *Pool {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()
	if c.pool == nil {
		max := c.poolMax
		if max < 2 {
			max = maxHotSize
		}
		ttl := c.poolTTL
		if ttl <= 0 {
			ttl = defaultIdleTTL
		}
		c.pool = NewPool(c, cfg, max, ttl)
		log.Printf("[claude-cli] process pool initialized (hot=%d, max=%d, ttl=%s)", defaultHotSize, max, ttl)
	}
	return c.pool
}

// resolveConfig builds a processConfig from the current client settings and request options.
func (c *Client) resolveConfig(model, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) processConfig {
	workdir := c.workdir
	configDir := ""
	sessionID := ""
	resume := false

	if opts != nil {
		if opts.WorkspaceDir != "" {
			workdir = opts.WorkspaceDir
			wsConfigDir := opts.WorkspaceDir + "/.claude-config"
			os.MkdirAll(wsConfigDir, 0755)
			configDir = wsConfigDir
		}
		sessionID = opts.SessionID
		resume = opts.Resume
	}

	return processConfig{
		workdir:         workdir,
		configDir:       configDir,
		sessionID:       sessionID,
		resume:          resume,
		model:           model,
		systemPrompt:    systemPrompt,
		maxTurns:        maxTurns,
		allowedTools:    allowedTools,
		mcpConfig:       mcpConfig,
		skipPermissions: c.skipPermissions,
	}
}

// conversationKey derives a pool key from request options.
// The relay passes channelID as part of the context, but the CLI client doesn't
// see it directly. We use workdir + sessionID as the conversation identity.
// The handler can set ConversationID on ChatOptions to provide an explicit key.
func conversationKey(opts *ChatOptions) string {
	if opts != nil && opts.ConversationID != "" {
		return opts.ConversationID
	}
	if opts != nil && opts.SessionID != "" {
		return "session:" + opts.SessionID
	}
	// Default: use a generic key (single-conversation mode).
	return "default"
}

// streamEvent covers the new flat stream-json format (claude --verbose).
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// result event (flat, new format)
	Result       string       `json:"result"`
	IsError      bool         `json:"is_error"`
	TotalCostUSD float64      `json:"total_cost_usd"`
	DurationMs   int64        `json:"duration_ms"`
	NumTurns     int          `json:"num_turns"`
	SessionID    string       `json:"session_id"`
	Usage        *resultUsage `json:"usage,omitempty"`

	// assistant event — carries the model name
	Message *assistantMessage `json:"message,omitempty"`
}

type assistantMessage struct {
	Model string `json:"model"`
}

type resultUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens"`
	CacheCreate  int `json:"cache_creation_input_tokens"`
}

// ChatResponse matches the common response format used by the handler.
type ChatResponse struct {
	Response     string
	Model        string
	InputTokens  int
	OutputTokens int
	CachedTokens int
	CostUSD      float64
	DurationMs   int64
	NumTurns     int
	SessionID    string
}

// ChatOptions holds per-request overrides for workspace and session routing.
type ChatOptions struct {
	WorkspaceDir   string // Override working directory (work disk mount path).
	SessionID      string // Resume an existing Claude session.
	Resume         bool   // Resume the most recent session (ignored if SessionID is set).
	ConversationID string // Explicit conversation key for pool routing.
}

// ChatCompletion sends a prompt and blocks until the full response is available.
func (c *Client) ChatCompletion(model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) (*ChatResponse, error) {
	cfg := c.resolveConfig(model, systemPrompt, maxTurns, allowedTools, mcpConfig, opts)
	pool := c.ensurePool(cfg)
	convKey := conversationKey(opts)

	proc, err := pool.Acquire(convKey)
	if err != nil {
		log.Printf("[claude-cli] pool acquire failed, falling back to one-shot: %v", err)
		return c.chatCompletionOneShot(model, prompt, systemPrompt, maxTurns, allowedTools, mcpConfig, opts)
	}

	resp, err := proc.sendMessage(prompt, nil)
	if err != nil {
		if !proc.alive {
			pool.MarkDead(convKey)
		}
		log.Printf("[claude-cli] persistent process error, falling back to one-shot: %v", err)
		return c.chatCompletionOneShot(model, prompt, systemPrompt, maxTurns, allowedTools, mcpConfig, opts)
	}

	pool.Release(convKey)

	if resp.Response == "" {
		return nil, fmt.Errorf("claude CLI produced no result")
	}
	return resp, nil
}

// ChatCompletionStream sends a prompt and streams events as they arrive.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		cfg := c.resolveConfig(model, systemPrompt, maxTurns, allowedTools, mcpConfig, opts)
		pool := c.ensurePool(cfg)
		convKey := conversationKey(opts)

		proc, err := pool.Acquire(convKey)
		if err != nil {
			log.Printf("[claude-cli] pool acquire failed, falling back to one-shot stream: %v", err)
			c.chatCompletionStreamOneShot(ctx, model, prompt, systemPrompt, maxTurns, allowedTools, mcpConfig, opts, ch)
			return
		}

		streamCb := func(ev StreamEvent) {
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}

		_, err = proc.sendMessage(prompt, streamCb)
		if err != nil {
			if !proc.alive {
				pool.MarkDead(convKey)
			}
			// Only send error if process died mid-stream (streamCb may have already sent partial data).
			if !proc.alive {
				ch <- StreamEvent{Err: err}
			}
			return
		}

		pool.Release(convKey)
		ch <- StreamEvent{Done: true}
	}()

	return ch
}

// --- One-shot fallback methods (original per-request exec pattern) ---

func (c *Client) chatCompletionOneShot(model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) (*ChatResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	isResume := opts != nil && (opts.SessionID != "" || opts.Resume)

	var args []string
	if isResume {
		if opts.SessionID != "" {
			args = append(args, "--session-id", opts.SessionID)
		} else {
			args = append(args, "--resume")
		}
		args = append(args, "-p", prompt, "--output-format", "stream-json", "--verbose")
	} else {
		args = []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
		}
	}

	if model != "" {
		args = append(args, "--model", model)
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}
	if mcpConfig != "" {
		args = append(args, "--mcp-config", mcpConfig)
	}
	if c.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	cmd := exec.CommandContext(ctx, c.binary, args...)

	workdir := c.workdir
	if opts != nil && opts.WorkspaceDir != "" {
		workdir = opts.WorkspaceDir
	}
	cmd.Dir = workdir

	env := c.env()
	if opts != nil && opts.WorkspaceDir != "" {
		wsConfigDir := opts.WorkspaceDir + "/.claude-config"
		os.MkdirAll(wsConfigDir, 0755)
		for i, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				env[i] = "CLAUDE_CONFIG_DIR=" + wsConfigDir
				break
			}
		}
	}
	cmd.Env = env

	if c.debug {
		log.Printf("[claude-cli] one-shot: %s %s", c.binary, strings.Join(args, " "))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %s", c.timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI exited with code %d: stderr=%s stdout=%s",
				exitErr.ExitCode(), stderr.String(), truncate(stdout.String(), 500))
		}
		return nil, fmt.Errorf("claude CLI exec: %w", err)
	}

	return parseStreamJSON(stdout.Bytes(), c.debug)
}

func (c *Client) chatCompletionStreamOneShot(ctx context.Context, model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions, ch chan<- StreamEvent) {
	isResume := opts != nil && (opts.SessionID != "" || opts.Resume)

	var args []string
	if isResume {
		if opts.SessionID != "" {
			args = append(args, "--session-id", opts.SessionID)
		} else {
			args = append(args, "--resume")
		}
		args = append(args, "-p", prompt, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	} else {
		args = []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--include-partial-messages",
		}
	}

	if model != "" {
		args = append(args, "--model", model)
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}
	if mcpConfig != "" {
		args = append(args, "--mcp-config", mcpConfig)
	}
	if c.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	cmd := exec.CommandContext(ctx, c.binary, args...)

	workdir := c.workdir
	if opts != nil && opts.WorkspaceDir != "" {
		workdir = opts.WorkspaceDir
	}
	cmd.Dir = workdir

	env := c.env()
	if opts != nil && opts.WorkspaceDir != "" {
		wsConfigDir := opts.WorkspaceDir + "/.claude-config"
		os.MkdirAll(wsConfigDir, 0755)
		for i, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				env[i] = "CLAUDE_CONFIG_DIR=" + wsConfigDir
				break
			}
		}
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ch <- StreamEvent{Err: fmt.Errorf("stdout pipe: %w", err)}
		return
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		ch <- StreamEvent{Err: fmt.Errorf("start claude: %w", err)}
		return
	}

	if c.debug {
		log.Printf("[claude-cli] one-shot stream: %s %s", c.binary, strings.Join(args, " "))
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var lastText string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant":
			if event.Message != nil && event.Message.Model != "" {
				ch <- StreamEvent{Model: event.Message.Model}
			}
			var raw struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &raw); err == nil {
				for _, block := range raw.Message.Content {
					if block.Type == "text" && block.Text != "" {
						if strings.HasPrefix(block.Text, lastText) && lastText != "" {
							delta := block.Text[len(lastText):]
							if delta != "" {
								ch <- StreamEvent{Text: delta}
							}
						} else if lastText == "" || !strings.HasPrefix(block.Text, lastText) {
							ch <- StreamEvent{Text: block.Text}
						}
						lastText = block.Text
					}
				}
			}

		case "result":
			if event.IsError {
				ch <- StreamEvent{Err: fmt.Errorf("claude CLI error: %s", event.Result)}
				return
			}
			if event.Result != "" && event.Result != lastText {
				if strings.HasPrefix(event.Result, lastText) && lastText != "" {
					remainder := event.Result[len(lastText):]
					if remainder != "" {
						ch <- StreamEvent{Text: remainder}
					}
				} else {
					ch <- StreamEvent{Text: event.Result}
				}
			}
			sev := StreamEvent{
				CostUSD:   event.TotalCostUSD,
				NumTurns:  event.NumTurns,
				SessionID: event.SessionID,
			}
			if event.Usage != nil {
				sev.Usage = &ChatResponseUsage{
					InputTokens:  event.Usage.InputTokens,
					OutputTokens: event.Usage.OutputTokens,
					CachedTokens: event.Usage.CacheRead,
				}
			}
			ch <- sev

		default:
			if c.debug {
				log.Printf("[claude-cli] event: %s", event.Type)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			ch <- StreamEvent{Err: fmt.Errorf("claude CLI timed out")}
			return
		}
		if lastText == "" {
			ch <- StreamEvent{Err: fmt.Errorf("claude CLI: %w", err)}
			return
		}
	}

	ch <- StreamEvent{Done: true}
}

// parseStreamJSON extracts the final result from stream-json output.
func parseStreamJSON(data []byte, debug bool) (*ChatResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var resp ChatResponse

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			if debug {
				log.Printf("[claude-cli] skip unparseable line: %s", truncate(line, 200))
			}
			continue
		}

		switch event.Type {
		case "result":
			if event.IsError {
				return nil, fmt.Errorf("claude CLI returned error: %s", event.Result)
			}
			resp.Response = event.Result
			resp.CostUSD = event.TotalCostUSD
			resp.DurationMs = event.DurationMs
			resp.NumTurns = event.NumTurns
			resp.SessionID = event.SessionID
			if event.Usage != nil {
				resp.InputTokens = event.Usage.InputTokens
				resp.OutputTokens = event.Usage.OutputTokens
				resp.CachedTokens = event.Usage.CacheRead
			}
		case "assistant":
			if event.Message != nil && event.Message.Model != "" {
				resp.Model = event.Message.Model
			}
		default:
			if debug {
				log.Printf("[claude-cli] event: %s", event.Type)
			}
		}
	}

	if resp.Response == "" {
		return nil, fmt.Errorf("claude CLI produced no result in output (%d bytes)", len(data))
	}

	return &resp, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// StreamEvent represents a single event from the Claude CLI stream.
type StreamEvent struct {
	Text      string
	Usage     *ChatResponseUsage
	CostUSD   float64
	NumTurns  int
	SessionID string
	Model     string
	Done      bool
	Err       error
}

// ChatResponseUsage holds token usage for streaming responses.
type ChatResponseUsage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
}
