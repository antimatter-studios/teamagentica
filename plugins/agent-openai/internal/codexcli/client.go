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

	// Persistent app-server process.
	serverMu  sync.Mutex
	server    *appServer
	initialized bool
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
	}
}

// SetTLS configures the CA certificate for Codex CLI MCP server connections.
func (c *Client) SetTLS(ca string) {
	c.tlsCA = ca
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

// ---------- App-server lifecycle ----------

// ensureServer starts the app-server if needed, sends initialize if fresh.
// Must be called with c.serverMu held.
func (c *Client) ensureServer() error {
	if c.server != nil && c.server.isAlive() {
		return nil
	}

	// Kill old server if dead.
	if c.server != nil {
		c.server.stop()
	}

	c.server = newAppServer(c.debug)
	c.initialized = false

	if err := c.server.start(c.binary, c.env()); err != nil {
		c.server = nil
		return fmt.Errorf("start app-server: %w", err)
	}

	// Send initialize handshake (clientInfo is required).
	_, err := c.server.sendRequest("initialize", map[string]interface{}{
		"clientInfo": map[string]string{
			"name":    "teamagentica-agent-openai",
			"version": "1.0.0",
		},
	})
	if err != nil {
		c.server.stop()
		c.server = nil
		return fmt.Errorf("initialize app-server: %w", err)
	}

	c.initialized = true
	if c.debug {
		log.Printf("[codex-cli] app-server initialized")
	}
	return nil
}

// StopServer gracefully stops the persistent app-server.
func (c *Client) StopServer() {
	c.serverMu.Lock()
	defer c.serverMu.Unlock()
	if c.server != nil {
		c.server.stop()
		c.server = nil
		c.initialized = false
	}
}

// ---------- Thread/Turn JSON-RPC types ----------

type threadStartParams struct {
	Model                 string `json:"model,omitempty"`
	Cwd                   string `json:"cwd,omitempty"`
	ApprovalPolicy        string `json:"approvalPolicy,omitempty"`
	Sandbox               string `json:"sandbox,omitempty"`
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
	Ephemeral             bool   `json:"ephemeral,omitempty"`
}

type threadStartResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model"`
}

// userInput represents a single input item in turn/start.
type userInput struct {
	Type string `json:"type"`           // "text", "image", "localImage"
	Text string `json:"text,omitempty"` // for type "text"
	URL  string `json:"url,omitempty"`  // for type "image"
	Path string `json:"path,omitempty"` // for type "localImage"
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	Model    string      `json:"model,omitempty"`
}

// ---------- Notification payload types ----------

type agentMessageDeltaParams struct {
	Delta    string `json:"delta"`
	ItemID   string `json:"itemId"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type itemCompletedParams struct {
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
		ID   string `json:"id"`
	} `json:"item"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		Usage *turnUsage `json:"usage,omitempty"`
	} `json:"turn"`
}

type turnUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

type errorNotificationParams struct {
	Message string `json:"message"`
}

// ---------- Chat Completion (blocking) ----------

// ChatCompletion runs a chat completion via the persistent app-server.
// workdirOverride, if non-empty, overrides the default working directory.
func (c *Client) ChatCompletion(model string, messages []openai.Message, imageURLs []string, workdirOverride string) (*openai.ChatResponse, error) {
	c.serverMu.Lock()
	defer c.serverMu.Unlock()

	if err := c.ensureServer(); err != nil {
		return nil, err
	}

	prompt := buildPrompt(messages)
	cwd := c.workdir
	if workdirOverride != "" {
		cwd = workdirOverride
	}

	// Download images to temp files for localImage input.
	var imagePaths []string
	for _, u := range imageURLs {
		path, err := downloadImage(u)
		if err != nil {
			log.Printf("[codex-cli] failed to download image %s: %v", u, err)
			continue
		}
		imagePaths = append(imagePaths, path)
		if c.debug {
			log.Printf("[codex-cli] downloaded image: %s → %s", u, path)
		}
	}
	defer func() {
		for _, p := range imagePaths {
			os.Remove(p)
		}
	}()

	// Start a new thread.
	threadID, err := c.startThread(model, cwd)
	if err != nil {
		// If thread start fails, kill the server so next call restarts fresh.
		c.server.stop()
		c.server = nil
		c.initialized = false
		return nil, fmt.Errorf("thread/start: %w", err)
	}

	// Build input items.
	input := buildUserInput(prompt, imagePaths)

	// Start the turn.
	if _, err := c.server.sendRequest("turn/start", turnStartParams{
		ThreadID: threadID,
		Input:    input,
		Model:    model,
	}); err != nil {
		return nil, fmt.Errorf("turn/start: %w", err)
	}

	// Collect notifications until turn completes.
	notifCh := make(chan notification, 256)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.server.readNotifications(notifCh)
		close(notifCh)
	}()

	var responseText string
	var usage openai.Usage

	for n := range notifCh {
		switch n.Method {
		case "notifications/itemCompleted":
			var p itemCompletedParams
			if json.Unmarshal(n.Params, &p) == nil && p.Item.Type == "agentMessage" && p.Item.Text != "" {
				responseText = p.Item.Text
			}
		case "notifications/turnCompleted":
			var p turnCompletedParams
			if json.Unmarshal(n.Params, &p) == nil && p.Turn.Usage != nil {
				usage.PromptTokens = p.Turn.Usage.InputTokens
				usage.CompletionTokens = p.Turn.Usage.OutputTokens
				usage.CachedTokens = p.Turn.Usage.CachedInputTokens
				usage.TotalTokens = p.Turn.Usage.InputTokens + p.Turn.Usage.OutputTokens
			}
		case "notifications/error":
			var p errorNotificationParams
			if json.Unmarshal(n.Params, &p) == nil && p.Message != "" {
				return nil, fmt.Errorf("codex: %s", p.Message)
			}
		default:
			if c.debug {
				log.Printf("[codex-cli] notification: %s", n.Method)
			}
		}
	}

	if err := <-errCh; err != nil {
		return nil, fmt.Errorf("reading turn notifications: %w", err)
	}

	if responseText == "" {
		return nil, fmt.Errorf("codex app-server produced no agent message")
	}

	return &openai.ChatResponse{
		ID: "codex-cli",
		Choices: []openai.Choice{
			{Message: openai.Message{Role: "assistant", Content: responseText}},
		},
		Usage: usage,
	}, nil
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

// buildUserInput constructs the input array for turn/start.
func buildUserInput(prompt string, imagePaths []string) []userInput {
	input := []userInput{{Type: "text", Text: prompt}}
	for _, p := range imagePaths {
		input = append(input, userInput{Type: "localImage", Path: p})
	}
	return input
}

// startThread sends thread/start and returns the thread ID.
func (c *Client) startThread(model, cwd string) (string, error) {
	result, err := c.server.sendRequest("thread/start", threadStartParams{
		Model:          model,
		Cwd:            cwd,
		ApprovalPolicy: "never",
		Sandbox:        "danger-full-access",
		Ephemeral:      true,
	})
	if err != nil {
		return "", err
	}

	var tsr threadStartResult
	if err := json.Unmarshal(result, &tsr); err != nil {
		return "", fmt.Errorf("parse thread/start result: %w", err)
	}
	if tsr.Thread.ID == "" {
		return "", fmt.Errorf("thread/start returned empty thread ID")
	}

	if c.debug {
		log.Printf("[codex-cli] thread started: %s (model=%s)", tsr.Thread.ID, tsr.Model)
	}
	return tsr.Thread.ID, nil
}

// ---------- Chat Completion Stream ----------

// StreamEvent represents a single event from the Codex CLI stream.
type StreamEvent struct {
	// Text contains an agent message chunk (may be empty).
	Text string
	// Usage is populated on turn completion.
	Usage *openai.Usage
	// Done is true when the stream is complete.
	Done bool
	// Err is set if an error occurred.
	Err error
}

// ChatCompletionStream runs a streaming chat completion via the persistent app-server.
// Returns a channel of StreamEvents. The channel is closed when the turn finishes.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, messages []openai.Message, imageURLs []string, workdirOverride string) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		c.serverMu.Lock()
		defer c.serverMu.Unlock()

		if err := c.ensureServer(); err != nil {
			ch <- StreamEvent{Err: err}
			return
		}

		prompt := buildPrompt(messages)
		cwd := c.workdir
		if workdirOverride != "" {
			cwd = workdirOverride
		}

		// Download images to temp files.
		var imagePaths []string
		for _, u := range imageURLs {
			path, err := downloadImage(u)
			if err != nil {
				log.Printf("[codex-cli] failed to download image %s: %v", u, err)
				continue
			}
			imagePaths = append(imagePaths, path)
		}
		defer func() {
			for _, p := range imagePaths {
				os.Remove(p)
			}
		}()

		// Start a new thread.
		threadID, err := c.startThread(model, cwd)
		if err != nil {
			c.server.stop()
			c.server = nil
			c.initialized = false
			ch <- StreamEvent{Err: fmt.Errorf("thread/start: %w", err)}
			return
		}

		// Build input items.
		input := buildUserInput(prompt, imagePaths)

		// Start the turn.
		if _, err := c.server.sendRequest("turn/start", turnStartParams{
			ThreadID: threadID,
			Input:    input,
			Model:    model,
		}); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("turn/start: %w", err)}
			return
		}

		// Read notifications and forward streaming events.
		notifCh := make(chan notification, 256)
		errCh := make(chan error, 1)
		go func() {
			errCh <- c.server.readNotifications(notifCh)
			close(notifCh)
		}()

		var lastText string

		for n := range notifCh {
			// Check context cancellation.
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Err: ctx.Err()}
				return
			default:
			}

			switch n.Method {
			case "notifications/agentMessageDelta":
				var p agentMessageDeltaParams
				if json.Unmarshal(n.Params, &p) == nil && p.Delta != "" {
					ch <- StreamEvent{Text: p.Delta}
					lastText += p.Delta
				}

			case "notifications/itemCompleted":
				var p itemCompletedParams
				if json.Unmarshal(n.Params, &p) == nil && p.Item.Type == "agentMessage" && p.Item.Text != "" {
					// Emit any remainder not covered by deltas.
					if lastText != "" && strings.HasPrefix(p.Item.Text, lastText) {
						remainder := p.Item.Text[len(lastText):]
						if remainder != "" {
							ch <- StreamEvent{Text: remainder}
						}
					} else if lastText == "" {
						// No deltas for this message (tool execution produced new message).
						ch <- StreamEvent{Text: p.Item.Text}
					}
					// Reset for the next agent message.
					lastText = ""
				}

			case "notifications/turnCompleted":
				var p turnCompletedParams
				if json.Unmarshal(n.Params, &p) == nil && p.Turn.Usage != nil {
					ch <- StreamEvent{
						Usage: &openai.Usage{
							PromptTokens:     p.Turn.Usage.InputTokens,
							CompletionTokens: p.Turn.Usage.OutputTokens,
							CachedTokens:     p.Turn.Usage.CachedInputTokens,
							TotalTokens:      p.Turn.Usage.InputTokens + p.Turn.Usage.OutputTokens,
						},
					}
				}

			case "notifications/error":
				var p errorNotificationParams
				if json.Unmarshal(n.Params, &p) == nil && p.Message != "" {
					ch <- StreamEvent{Err: fmt.Errorf("codex: %s", p.Message)}
					return
				}

			default:
				if c.debug {
					log.Printf("[codex-cli] notification: %s", n.Method)
				}
			}
		}

		if err := <-errCh; err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("reading turn notifications: %w", err)}
			return
		}

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
