package kimicli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// Client wraps the kimi-cli Python binary for chat completions via wire protocol.
type Client struct {
	binary   string
	workdir  string
	kimiHome string // KIMI_SHARE_DIR
	timeout  time.Duration
	debug    bool
}

// NewClient creates a new Kimi CLI client.
func NewClient(binary, workdir, kimiHome string, timeoutSec int, debug bool) *Client {
	return &Client{
		binary:   binary,
		workdir:  workdir,
		kimiHome: kimiHome,
		timeout:  time.Duration(timeoutSec) * time.Second,
		debug:    debug,
	}
}

func (c *Client) env() []string {
	env := os.Environ()
	env = append(env, "KIMI_SHARE_DIR="+c.kimiHome)
	env = append(env, "PYTHONUNBUFFERED=1")
	return env
}

// IsAuthenticated checks if the kimi-cli config exists.
func (c *Client) IsAuthenticated() bool {
	_, err := os.Stat(c.kimiHome + "/config.toml")
	return err == nil
}

// StreamEvent represents a single event from the Kimi CLI wire protocol.
type StreamEvent struct {
	Text  string // Agent message chunk.
	Done  bool   // True when stream is complete.
	Err   error  // Set if an error occurred.
	Usage *Usage // Token usage info from StatusUpdate.
}

// Usage holds token count information from the wire protocol StatusUpdate.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CachedTokens     int
	CacheCreation    int
	TotalTokens      int
	ContextTokens    int
	MaxContextTokens int
}

// wireMessage is the JSON-RPC envelope for wire protocol messages.
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	ID      string          `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// wireEvent wraps the event type and payload from the wire protocol.
type wireEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type contentPartPayload struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type statusUpdatePayload struct {
	ContextUsage     float64    `json:"context_usage"`
	ContextTokens    int        `json:"context_tokens"`
	MaxContextTokens int        `json:"max_context_tokens"`
	TokenUsage       *tokenUsag `json:"token_usage"`
}

type tokenUsag struct {
	InputOther         int `json:"input_other"`
	Output             int `json:"output"`
	InputCacheRead     int `json:"input_cache_read"`
	InputCacheCreation int `json:"input_cache_creation"`
}

// ChatCompletionStream runs kimi CLI in wire mode and streams events.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, prompt string, mcpConfigFile string) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		args := []string{"--wire", "--yolo"}
		if model != "" {
			args = append(args, "--model", model)
		}
		if mcpConfigFile != "" {
			args = append(args, "--mcp-config-file", mcpConfigFile)
		}

		cmd := exec.CommandContext(ctx, c.binary, args...)
		cmd.Dir = c.workdir
		cmd.Env = c.env()

		stdin, err := cmd.StdinPipe()
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("stdin pipe: %w", err)}
			return
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("stdout pipe: %w", err)}
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("stderr pipe: %w", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("start kimi: %w", err)}
			return
		}

		// Drain stderr in background.
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				log.Printf("[kimi-cli] stderr: %s", scanner.Text())
			}
		}()

		if c.debug {
			log.Printf("[kimi-cli] wire: %s %s", c.binary, strings.Join(args, " "))
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

		// Step 1: Send initialize.
		initMsg := `{"jsonrpc":"2.0","method":"initialize","id":"1","params":{"protocol_version":"1.8","client":{"name":"teamagentica","version":"1.0"},"capabilities":{"supports_question":false}}}`
		if _, err := fmt.Fprintln(stdin, initMsg); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("write initialize: %w", err)}
			return
		}

		// Wait for initialize response.
		var initialized atomic.Bool
		for scanner.Scan() {
			line := scanner.Text()
			if c.debug {
				log.Printf("[kimi-cli] wire-init: %s", truncate(line, 200))
			}
			var msg wireMessage
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}
			if msg.ID == "1" && msg.Result != nil {
				initialized.Store(true)
				break
			}
			if msg.Error != nil {
				ch <- StreamEvent{Err: fmt.Errorf("initialize error: %s", msg.Error.Message)}
				return
			}
		}

		if !initialized.Load() {
			ch <- StreamEvent{Err: fmt.Errorf("kimi CLI did not respond to initialize")}
			return
		}

		// Step 2: Send prompt.
		promptJSON, _ := json.Marshal(prompt)
		promptMsg := fmt.Sprintf(`{"jsonrpc":"2.0","method":"prompt","id":"2","params":{"user_input":%s}}`, string(promptJSON))
		if _, err := fmt.Fprintln(stdin, promptMsg); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("write prompt: %w", err)}
			return
		}

		// Step 3: Read events until TurnEnd or prompt result.
		for scanner.Scan() {
			line := scanner.Text()
			if c.debug {
				log.Printf("[kimi-cli] wire: %s", truncate(line, 300))
			}

			var msg wireMessage
			if json.Unmarshal([]byte(line), &msg) != nil {
				continue
			}

			// Handle prompt completion response.
			if msg.ID == "2" {
				if msg.Error != nil {
					ch <- StreamEvent{Err: fmt.Errorf("prompt error: %s", msg.Error.Message)}
				}
				break
			}

			// Handle events.
			if msg.Method == "event" && msg.Params != nil {
				var ev wireEvent
				if json.Unmarshal(msg.Params, &ev) != nil {
					continue
				}

				switch ev.Type {
				case "ContentPart":
					var cp contentPartPayload
					if json.Unmarshal(ev.Payload, &cp) == nil && cp.Type == "text" && cp.Text != "" {
						ch <- StreamEvent{Text: cp.Text}
					}

				case "StatusUpdate":
					var su statusUpdatePayload
					if json.Unmarshal(ev.Payload, &su) == nil && su.TokenUsage != nil {
						totalInput := su.TokenUsage.InputOther + su.TokenUsage.InputCacheRead + su.TokenUsage.InputCacheCreation
						ch <- StreamEvent{
							Usage: &Usage{
								InputTokens:      totalInput,
								OutputTokens:     su.TokenUsage.Output,
								CachedTokens:     su.TokenUsage.InputCacheRead,
								CacheCreation:    su.TokenUsage.InputCacheCreation,
								TotalTokens:      totalInput + su.TokenUsage.Output,
								ContextTokens:    su.ContextTokens,
								MaxContextTokens: su.MaxContextTokens,
							},
						}
					}

				case "TurnEnd":
					// Turn complete, wait for prompt result response.

				default:
					if c.debug {
						log.Printf("[kimi-cli] event: %s", ev.Type)
					}
				}
			}

			// Handle approval requests by auto-approving (yolo mode should handle this,
			// but just in case).
			if msg.Method == "request" && msg.Params != nil {
				var req struct {
					Type    string `json:"type"`
					Payload struct {
						ID string `json:"id"`
					} `json:"payload"`
				}
				if json.Unmarshal(msg.Params, &req) == nil && req.Payload.ID != "" {
					approveMsg := fmt.Sprintf(`{"jsonrpc":"2.0","method":"approve","id":"approve-%s","params":{"request_id":"%s","response":"approve"}}`,
						req.Payload.ID, req.Payload.ID)
					fmt.Fprintln(stdin, approveMsg)
				}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("read wire: %w", err)}
		}

		// Close stdin to signal we're done, then wait for process.
		stdin.Close()
		cmd.Wait()

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
