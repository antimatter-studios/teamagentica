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
	binary          string
	workdir         string
	claudeDir       string // CLAUDE_CONFIG_DIR equivalent
	timeout         time.Duration
	debug           bool
	skipPermissions bool
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

// streamEvent covers the new flat stream-json format (claude --verbose).
// The "result" event now has the response text directly in the "result" string
// field rather than nested under a "result" object.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// result event (flat, new format)
	Result      string      `json:"result"`       // response text
	IsError     bool        `json:"is_error"`
	TotalCostUSD float64    `json:"total_cost_usd"`
	DurationMs  int64       `json:"duration_ms"`
	NumTurns    int         `json:"num_turns"`
	SessionID   string      `json:"session_id"`
	Usage       *resultUsage `json:"usage,omitempty"`

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
	// Text contains a content delta (may be empty).
	Text string
	// Usage is populated on the result event.
	Usage *ChatResponseUsage
	// CostUSD from the result event.
	CostUSD float64
	// NumTurns from the result event.
	NumTurns int
	// SessionID from the result event.
	SessionID string
	// Model from the assistant event.
	Model string
	// Done is true when the stream is complete.
	Done bool
	// Err is set if an error occurred.
	Err error
}

// ChatResponseUsage holds token usage for streaming responses.
type ChatResponseUsage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
}

// ChatCompletionStream runs the Claude CLI with --include-partial-messages and
// streams events as they arrive. Returns a channel of StreamEvents.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, prompt string, systemPrompt string, maxTurns int, allowedTools []string, mcpConfig string, opts *ChatOptions) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

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
			log.Printf("[claude-cli] streaming: %s %s", c.binary, strings.Join(args, " "))
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
				// Partial or complete assistant message — extract text content.
				if event.Message != nil && event.Message.Model != "" {
					ch <- StreamEvent{Model: event.Message.Model}
				}
				// Extract text from message content blocks.
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
							// Emit delta — text we haven't sent yet.
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
				// Emit any remaining text not yet sent via deltas.
				if event.Result != "" && event.Result != lastText {
					if strings.HasPrefix(event.Result, lastText) && lastText != "" {
						remainder := event.Result[len(lastText):]
						if remainder != "" {
							ch <- StreamEvent{Text: remainder}
						}
					} else if lastText == "" {
						ch <- StreamEvent{Text: event.Result}
					}
				}
				// Emit usage/metadata.
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
	}()

	return ch
}
