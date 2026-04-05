package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
)


// LoginResult holds the device-code login details for the UI.
type LoginResult struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

// Client wraps the Codex CLI binary for chat completions.
type Client struct {
	binary    string
	workdir   string
	codexHome string
	timeout   time.Duration
	debug     bool
	tlsCA     string // CA cert path for MCP server connections.

	// Active login process state.
	loginMu   sync.Mutex
	loginProc *exec.Cmd
	loginDone chan error

	// Persistent app-server.
	server   *appServer
	serverMu sync.Mutex // serializes chat requests through the app-server

	// Thread cache: reuse threads across messages to avoid 5s thread/start cost.
	threads   map[string]string // channelID → threadID
	threadsMu sync.Mutex
}

// NewClient creates a new Codex CLI client. codexHome is the path for
// CODEX_HOME where the CLI stores auth tokens and config.
func NewClient(binary, workdir, codexHome string, timeoutSec int, debug bool) *Client {
	return &Client{
		binary:    binary,
		workdir:   workdir,
		codexHome: codexHome,
		timeout:   time.Duration(timeoutSec) * time.Second,
		debug:     debug,
		threads:   make(map[string]string),
	}
}

// SetTLS configures the CA certificate for Codex CLI MCP server connections.
func (c *Client) SetTLS(ca string) {
	c.tlsCA = ca
}

// StartAppServer starts the persistent app-server subprocess.
// Call once at plugin startup. The server is not yet used for chat — this
// only tests that the process starts and stays alive.
func (c *Client) StartAppServer() error {
	s := &appServer{debug: c.debug}
	if err := s.start(c.binary, c.env()); err != nil {
		return err
	}
	log.Printf("[codex-cli] app-server process started, sending initialize...")
	if err := s.initialize(); err != nil {
		s.stop()
		return fmt.Errorf("initialize: %w", err)
	}
	c.server = s
	log.Printf("[codex-cli] app-server initialized OK")
	return nil
}

// StopAppServer stops the persistent app-server subprocess.
func (c *Client) StopAppServer() {
	if c.server != nil {
		c.server.stop()
		c.server = nil
	}
}

func (c *Client) env() []string {
	env := append(os.Environ(), "CODEX_HOME="+c.codexHome)
	if c.tlsCA != "" {
		env = append(env, "SSL_CERT_FILE="+c.tlsCA)
	}
	return env
}

// IsAuthenticated checks if the Codex CLI has stored credentials.
func (c *Client) IsAuthenticated() bool {
	cmd := exec.Command(c.binary, "login", "status")
	cmd.Env = c.env()
	return cmd.Run() == nil
}

// ListModels reads available models from the Codex CLI models_cache.json file.
// Returns model slugs sorted alphabetically, or an error if the cache is unavailable.
func (c *Client) ListModels() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(c.codexHome, "models_cache.json"))
	if err != nil {
		return nil, fmt.Errorf("read models cache: %w", err)
	}
	var cache struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parse models cache: %w", err)
	}
	result := make([]string, 0, len(cache.Models))
	for _, m := range cache.Models {
		result = append(result, m.Slug)
	}
	sort.Strings(result)
	return result, nil
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	urlRe  = regexp.MustCompile(`(https://auth\.openai\.com/\S+)`)
	codeRe = regexp.MustCompile(`\b([A-Z0-9]{4}-[A-Z0-9]{4,5})\b`)
)

// StartLogin spawns `codex login --device-auth` in the background, parses the
// URL and one-time code from its output, and returns them. The process stays
// alive waiting for the user to complete login in the browser.
func (c *Client) StartLogin() (*LoginResult, error) {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()

	// Kill any existing login process.
	if c.loginProc != nil && c.loginProc.Process != nil {
		c.loginProc.Process.Kill()
		c.loginProc = nil
		c.loginDone = nil
	}

	cmd := exec.Command(c.binary, "login", "--device-auth")
	cmd.Env = c.env()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex login: %w", err)
	}

	c.loginProc = cmd
	done := make(chan error, 1)
	c.loginDone = done

	// Read stdout until we have both URL and code, with a timeout.
	resultCh := make(chan *LoginResult, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		var url, code string
		for scanner.Scan() {
			raw := scanner.Text()
			line := ansiRe.ReplaceAllString(raw, "")
			log.Printf("[codex-cli] login: %s", line)
			if url == "" {
				if m := urlRe.FindString(line); m != "" {
					url = m
				}
			}
			if code == "" {
				if m := codeRe.FindString(line); m != "" {
					code = m
				}
			}
			if url != "" && code != "" {
				resultCh <- &LoginResult{URL: url, Code: code}
				break
			}
		}
		// Keep draining stdout so the process doesn't block.
		go func() {
			for scanner.Scan() {
				// discard
			}
		}()
		// Wait for process exit in background.
		done <- cmd.Wait()
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("codex login exited before providing code: %w", err)
		}
		return nil, fmt.Errorf("codex login exited before providing URL and code")
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		return nil, fmt.Errorf("timed out waiting for login URL/code from codex CLI")
	}
}

// PollLogin checks whether the background login process completed successfully.
// Returns (true, nil) if authenticated, (false, nil) if still waiting.
func (c *Client) PollLogin() (bool, error) {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()

	if c.loginDone == nil {
		// No login in progress — just check status.
		return c.IsAuthenticated(), nil
	}

	select {
	case err := <-c.loginDone:
		c.loginProc = nil
		c.loginDone = nil
		if err != nil {
			return false, fmt.Errorf("codex login failed: %w", err)
		}
		return true, nil
	default:
		// Still waiting for user to complete browser auth.
		return false, nil
	}
}

// Logout clears stored Codex CLI credentials.
func (c *Client) Logout() error {
	cmd := exec.Command(c.binary, "logout")
	cmd.Env = c.env()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex logout: %w: %s", err, string(output))
	}
	return nil
}


// downloadImage fetches a URL to a temp file and returns the path.
func downloadImage(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image download returned status %d", resp.StatusCode)
	}

	// Determine extension from URL or default to .jpg.
	ext := filepath.Ext(url)
	if ext == "" || len(ext) > 5 {
		ext = ".jpg"
	}

	f, err := os.CreateTemp("", "teamagentica-img-*"+ext)
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("writing image: %w", err)
	}

	return f.Name(), nil
}

// buildPrompt concatenates conversation messages into a single prompt string.
func buildPrompt(messages []openai.Message) string {
	if len(messages) == 1 {
		return messages[0].Content
	}

	var sb strings.Builder
	for i, msg := range messages {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch msg.Role {
		case "user":
			sb.WriteString("User: ")
		case "assistant":
			sb.WriteString("Assistant: ")
		case "system":
			sb.WriteString("System: ")
		}
		sb.WriteString(msg.Content)
	}
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// StreamEvent represents a single event from the Codex CLI JSONL stream.
type StreamEvent struct {
	// Text contains an agent message chunk (may be empty).
	Text string
	// Usage is populated on turn.completed.
	Usage *openai.Usage
	// Done is true when the stream is complete.
	Done bool
	// Err is set if an error occurred.
	Err error
}

// ChatCompletionStream streams a chat completion via the persistent app-server.
// sessionID identifies the conversation — threads are reused across messages with the same sessionID.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, messages []openai.Message, imageURLs []string, workdirOverride string, sessionID string) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		// Serialize — stdio is single-channel, one request at a time.
		c.serverMu.Lock()
		defer c.serverMu.Unlock()

		prompt := buildPrompt(messages)
		cwd := c.workdir
		if workdirOverride != "" {
			cwd = workdirOverride
		}

		start := time.Now()
		mark := func(label string) string {
			return fmt.Sprintf("%s=%dms", label, time.Since(start).Milliseconds())
		}

		// Reuse thread for same session, or create a new one.
		c.threadsMu.Lock()
		threadID := c.threads[sessionID]
		c.threadsMu.Unlock()

		if threadID == "" {
			log.Printf("[codex-cli] app-server: creating thread (model=%s session=%s)", model, sessionID)
			threadResult, err := c.server.sendRequest("thread/start", map[string]interface{}{
				"model":          model,
				"cwd":            cwd,
				"approvalPolicy": "never",
				"sandbox":        "danger-full-access",
			}, nil)
			if err != nil {
				ch <- StreamEvent{Err: fmt.Errorf("thread/start: %w", err)}
				return
			}
			var tsr struct {
				Thread struct{ ID string } `json:"thread"`
			}
			if err := json.Unmarshal(threadResult, &tsr); err != nil || tsr.Thread.ID == "" {
				ch <- StreamEvent{Err: fmt.Errorf("bad thread/start response")}
				return
			}
			threadID = tsr.Thread.ID
			c.threadsMu.Lock()
			c.threads[sessionID] = threadID
			c.threadsMu.Unlock()
		} else {
			log.Printf("[codex-cli] app-server: reusing thread %s (session=%s)", threadID, sessionID)
		}
		tThread := mark("thread")

		// Build input.
		input := []map[string]string{{"type": "text", "text": prompt}}

		// turn/start
		firstToken := time.Time{}
		_, err := c.server.sendRequest("turn/start", map[string]interface{}{
			"threadId": threadID,
			"input":    input,
			"model":    model,
		}, func(n notification) {
			if firstToken.IsZero() && n.Method == "item/agentMessage/delta" {
				firstToken = time.Now()
			}
			c.handleNotification(n, ch)
		})
		if err != nil {
			log.Printf("[codex-cli] app-server turn/start failed: %v", err)
			ch <- StreamEvent{Err: fmt.Errorf("turn/start: %w", err)}
			return
		}
		tTurnStart := mark("turn_start")

		// Read notifications until turn completes.
		err = c.server.readNotifications(func(n notification) {
			if firstToken.IsZero() && n.Method == "item/agentMessage/delta" {
				firstToken = time.Now()
			}
			c.handleNotification(n, ch)
		})
		if err != nil {
			ch <- StreamEvent{Err: err}
			return
		}
		tTurnDone := mark("turn_done")

		firstTokenMs := ""
		if !firstToken.IsZero() {
			firstTokenMs = fmt.Sprintf("first_token=%dms", firstToken.Sub(start).Milliseconds())
		}
		log.Printf("[codex-cli] [timing] %s %s %s %s", tThread, tTurnStart, firstTokenMs, tTurnDone)
		ch <- StreamEvent{Done: true}
	}()

	return ch
}

// handleNotification processes a single app-server notification into StreamEvents.
func (c *Client) handleNotification(n notification, ch chan<- StreamEvent) {
	switch n.Method {
	case "item/agentMessage/delta":
		// v2 protocol delta — use this one (ignore codex/event/agent_message_delta to avoid duplicates).
		var p struct{ Delta string }
		if json.Unmarshal(n.Params, &p) == nil && p.Delta != "" {
			ch <- StreamEvent{Text: p.Delta}
		}
	case "codex/event/token_count":
		var p struct {
			Msg struct {
				Info struct {
					TotalTokenUsage struct {
						InputTokens       int `json:"input_tokens"`
						CachedInputTokens int `json:"cached_input_tokens"`
						OutputTokens      int `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"msg"`
		}
		if json.Unmarshal(n.Params, &p) == nil {
			u := p.Msg.Info.TotalTokenUsage
			ch <- StreamEvent{Usage: &openai.Usage{
				PromptTokens:     u.InputTokens,
				CompletionTokens: u.OutputTokens,
				CachedTokens:     u.CachedInputTokens,
				TotalTokens:      u.InputTokens + u.OutputTokens,
			}}
		}
	case "turn/completed", "codex/event/task_complete":
		// Terminal — handled by readNotifications loop exit.
	case "notifications/error":
		var p struct{ Message string }
		if json.Unmarshal(n.Params, &p) == nil && p.Message != "" {
			ch <- StreamEvent{Err: fmt.Errorf("codex: %s", p.Message)}
		}
	}
}

