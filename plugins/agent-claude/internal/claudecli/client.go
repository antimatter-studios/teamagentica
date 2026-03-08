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
	"time"
)

// Client wraps the Claude Code CLI binary for chat completions.
type Client struct {
	binary    string
	workdir   string
	claudeDir string // CLAUDE_CONFIG_DIR equivalent
	timeout   time.Duration
	debug     bool
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

func (c *Client) env() []string {
	env := os.Environ()
	env = append(env, "CLAUDE_CONFIG_DIR="+c.claudeDir)
	// Disable interactive prompts.
	env = append(env, "CI=1")
	return env
}

// IsAvailable checks if the Claude CLI binary exists and is runnable.
func (c *Client) IsAvailable() bool {
	cmd := exec.Command(c.binary, "--version")
	cmd.Env = c.env()
	return cmd.Run() == nil
}

// StreamJSON event types from claude --output-format stream-json.

type streamEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`

	// result fields (type == "result")
	Result *resultData `json:"result,omitempty"`

	// For content_block_delta
	Delta *deltaData `json:"delta,omitempty"`

	// For system messages
	Subtype string `json:"subtype,omitempty"`
}

type resultData struct {
	Type         string        `json:"type"`
	Role         string        `json:"role"`
	Content      []contentItem `json:"content"`
	StopReason   string        `json:"stop_reason"`
	Usage        *resultUsage  `json:"usage,omitempty"`
	Model        string        `json:"model"`
	CostUSD      float64       `json:"cost_usd"`
	DurationMs   int64         `json:"duration_ms"`
	IsError      bool          `json:"is_error"`
	SessionID    string        `json:"session_id"`
	NumTurns     int           `json:"num_turns"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type deltaData struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
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
	WorkspaceDir string // Override working directory (work disk mount path).
	SessionID    string // Resume an existing Claude session.
	Resume       bool   // Resume the most recent session (ignored if SessionID is set).
}

// ChatCompletion runs claude -p with stream-json output and parses the result.
func (c *Client) ChatCompletion(model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) (*ChatResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Determine whether this is a new session or a continuation.
	isResume := opts != nil && (opts.SessionID != "" || opts.Resume)

	var args []string
	if isResume {
		// Resume mode: use --resume or --session-id with -p for the new message.
		if opts.SessionID != "" {
			args = append(args, "--session-id", opts.SessionID)
		} else {
			args = append(args, "--resume")
		}
		args = append(args, "-p", prompt, "--output-format", "stream-json")
	} else {
		args = []string{
			"-p", prompt,
			"--output-format", "stream-json",
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

	cmd := exec.CommandContext(ctx, c.binary, args...)

	// Use workspace override if provided, otherwise fall back to default workdir.
	workdir := c.workdir
	if opts != nil && opts.WorkspaceDir != "" {
		workdir = opts.WorkspaceDir
	}
	cmd.Dir = workdir

	// If a workspace-specific config dir exists, use it for session isolation.
	env := c.env()
	if opts != nil && opts.WorkspaceDir != "" {
		// Each workspace gets its own config dir so sessions don't collide.
		wsConfigDir := opts.WorkspaceDir + "/.claude-config"
		os.MkdirAll(wsConfigDir, 0755)
		// Replace the default CLAUDE_CONFIG_DIR with the workspace-scoped one.
		for i, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				env[i] = "CLAUDE_CONFIG_DIR=" + wsConfigDir
				break
			}
		}
	}
	cmd.Env = env

	if c.debug {
		log.Printf("[claude-cli] running: %s %s", c.binary, strings.Join(args, " "))
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

// parseStreamJSON extracts the final result from stream-json output.
func parseStreamJSON(data []byte, debug bool) (*ChatResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase buffer for potentially large outputs.
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
			if event.Result != nil {
				// Extract text from content blocks.
				var texts []string
				for _, c := range event.Result.Content {
					if c.Type == "text" && c.Text != "" {
						texts = append(texts, c.Text)
					}
				}
				resp.Response = strings.Join(texts, "\n")
				resp.Model = event.Result.Model
				resp.CostUSD = event.Result.CostUSD
				resp.DurationMs = event.Result.DurationMs
				resp.NumTurns = event.Result.NumTurns
				resp.SessionID = event.Result.SessionID

				if event.Result.Usage != nil {
					resp.InputTokens = event.Result.Usage.InputTokens
					resp.OutputTokens = event.Result.Usage.OutputTokens
					resp.CachedTokens = event.Result.Usage.CacheRead
				}

				if event.Result.IsError {
					return nil, fmt.Errorf("claude CLI returned error: %s", resp.Response)
				}
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
