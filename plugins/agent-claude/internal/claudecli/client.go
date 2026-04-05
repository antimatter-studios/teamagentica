package claudecli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
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
		return nil, fmt.Errorf("pool acquire: %w", err)
	}

	resp, err := proc.sendMessage(prompt, nil)
	if err != nil {
		if !proc.alive {
			pool.MarkDead(convKey)
		}
		return nil, err
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
		start := time.Now()

		cfg := c.resolveConfig(model, systemPrompt, maxTurns, allowedTools, mcpConfig, opts)
		pool := c.ensurePool(cfg)
		convKey := conversationKey(opts)

		proc, err := pool.Acquire(convKey)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("pool acquire: %w", err)}
			return
		}
		acquireMs := time.Since(start).Milliseconds()

		firstToken := time.Time{}
		streamCb := func(ev StreamEvent) {
			if firstToken.IsZero() && ev.Text != "" {
				firstToken = time.Now()
			}
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
			ch <- StreamEvent{Err: err}
			return
		}

		pool.Release(convKey)

		firstTokenMs := ""
		if !firstToken.IsZero() {
			firstTokenMs = fmt.Sprintf("first_token=%dms", firstToken.Sub(start).Milliseconds())
		}
		log.Printf("[claude-cli] [timing] acquire=%dms %s total=%dms",
			acquireMs, firstTokenMs, time.Since(start).Milliseconds())
		ch <- StreamEvent{Done: true}
	}()

	return ch
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
	ToolName  string // non-empty when Claude starts using a tool (e.g. "Bash", "Edit", "Read")
	ToolDone  string // non-empty when tool result arrives (tool name)
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
