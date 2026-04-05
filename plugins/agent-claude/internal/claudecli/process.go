package claudecli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/uuid"
)

func generateUUID() string {
	return uuid.New().String()
}

// processConfig captures the startup parameters for a persistent CLI process.
// If any of these change between requests, the process must be restarted.
type processConfig struct {
	workdir         string
	configDir       string // resolved CLAUDE_CONFIG_DIR (may be workspace-scoped)
	sessionID       string
	resume          bool
	model           string
	systemPrompt    string
	maxTurns        int
	allowedTools    []string
	mcpConfig       string
	skipPermissions bool
}

// process manages a single long-lived Claude CLI subprocess.
type process struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Scanner
	alive  bool
	cfg    processConfig
	client *Client // back-reference for env/binary
}

// startProcess spawns a new persistent Claude CLI subprocess.
func (c *Client) startProcess(cfg processConfig) (*process, error) {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}

	if cfg.sessionID != "" {
		args = append(args, "--session-id", cfg.sessionID)
	} else if cfg.resume {
		args = append(args, "--resume")
	}
	if cfg.model != "" {
		args = append(args, "--model", cfg.model)
	}
	if cfg.systemPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.systemPrompt)
	}
	if cfg.maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", cfg.maxTurns))
	}
	for _, tool := range cfg.allowedTools {
		args = append(args, "--allowedTools", tool)
	}
	if cfg.mcpConfig != "" {
		args = append(args, "--mcp-config", cfg.mcpConfig)
	}
	if cfg.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	cmd := exec.Command(c.binary, args...)
	cmd.Dir = cfg.workdir

	// Build environment.
	env := c.env()
	if cfg.configDir != "" {
		for i, e := range env {
			if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
				env[i] = "CLAUDE_CONFIG_DIR=" + cfg.configDir
				break
			}
		}
	}
	cmd.Env = env
	cmd.Stderr = os.Stderr // let CLI errors flow to plugin logs

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude CLI: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	log.Printf("[claude-cli] persistent process started: pid=%d args=%s", cmd.Process.Pid, strings.Join(args, " "))

	p := &process{
		cmd:    cmd,
		stdin:  stdinPipe,
		stdout: stdoutPipe,
		reader: scanner,
		alive:  true,
		cfg:    cfg,
		client: c,
	}

	return p, nil
}

// kill terminates the subprocess.
func (p *process) kill() {
	p.alive = false
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		log.Printf("[claude-cli] killing persistent process pid=%d", p.cmd.Process.Pid)
		p.cmd.Process.Kill()
		p.cmd.Wait()
	}
}

// streamInputMessage matches the Agent SDK envelope for stream-json input.
type streamInputMessage struct {
	Type              string             `json:"type"`
	UUID              string             `json:"uuid"`
	SessionID         string             `json:"session_id"`
	ParentToolUseID   *string            `json:"parent_tool_use_id"`
	Message           streamInputPayload `json:"message"`
}

type streamInputPayload struct {
	Role    string               `json:"role"`
	Content []streamContentBlock `json:"content"`
}

type streamContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sendMessage writes a user message to the process stdin and reads events until
// a "result" event arrives. Returns all events (including intermediates) via
// the provided callback, or collects and returns the final ChatResponse.
func (p *process) sendMessage(prompt string, streamCb func(StreamEvent)) (*ChatResponse, error) {
	msg := streamInputMessage{
		Type:      "user",
		UUID:      generateUUID(),
		SessionID: "",
		Message: streamInputPayload{
			Role: "user",
			Content: []streamContentBlock{
				{Type: "text", Text: prompt},
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}
	data = append(data, '\n')

	if _, err := p.stdin.Write(data); err != nil {
		p.alive = false
		return nil, fmt.Errorf("write to stdin (process dead): %w", err)
	}

	if p.client.debug {
		log.Printf("[claude-cli] sent message to persistent process (%d bytes)", len(data))
	}

	// Read events until we get a "result" type.
	var resp ChatResponse
	var lastText string

	for p.reader.Scan() {
		line := strings.TrimSpace(p.reader.Text())
		if line == "" {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			if p.client.debug {
				log.Printf("[claude-cli] skip unparseable line: %s", truncate(line, 200))
			}
			continue
		}

		switch event.Type {
		case "assistant":
			if event.Message != nil && event.Message.Model != "" {
				resp.Model = event.Message.Model
				if streamCb != nil {
					streamCb(StreamEvent{Model: event.Message.Model})
				}
			}
			// Extract content blocks — text deltas and tool use.
			var raw struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
						Name string `json:"name"` // tool_use name
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &raw); err == nil {
				for _, block := range raw.Message.Content {
					if block.Type == "tool_use" && block.Name != "" && streamCb != nil {
						streamCb(StreamEvent{ToolName: block.Name})
					}
					if block.Type == "text" && block.Text != "" {
						if streamCb != nil {
							// Emit delta.
							if strings.HasPrefix(block.Text, lastText) && lastText != "" {
								delta := block.Text[len(lastText):]
								if delta != "" {
									streamCb(StreamEvent{Text: delta})
								}
							} else if lastText == "" || !strings.HasPrefix(block.Text, lastText) {
								streamCb(StreamEvent{Text: block.Text})
							}
						}
						lastText = block.Text
					}
				}
			}

		case "result":
			if event.IsError {
				errMsg := fmt.Errorf("claude CLI error: %s", event.Result)
				if streamCb != nil {
					streamCb(StreamEvent{Err: errMsg})
				}
				return nil, errMsg
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

			if streamCb != nil {
				// Emit any remaining text delta.
				if event.Result != "" && event.Result != lastText {
					if strings.HasPrefix(event.Result, lastText) && lastText != "" {
						remainder := event.Result[len(lastText):]
						if remainder != "" {
							streamCb(StreamEvent{Text: remainder})
						}
					} else {
						streamCb(StreamEvent{Text: event.Result})
					}
				}
				// Emit usage metadata.
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
				streamCb(sev)
			}

			return &resp, nil

		default:
			if p.client.debug {
				log.Printf("[claude-cli] event: %s", event.Type)
			}
		}
	}

	// Scanner exited without a result — process died.
	p.alive = false
	if err := p.reader.Err(); err != nil {
		return nil, fmt.Errorf("claude CLI process exited unexpectedly (scanner error: %w)", err)
	}
	return nil, fmt.Errorf("claude CLI process exited unexpectedly (no result event)")
}
