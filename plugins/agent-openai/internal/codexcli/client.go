package codexcli

import (
	"bufio"
	"bytes"
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

	// Active login process state.
	loginMu   sync.Mutex
	loginProc *exec.Cmd
	loginDone chan error
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

func (c *Client) env() []string {
	return append(os.Environ(), "CODEX_HOME="+c.codexHome)
}

// IsAuthenticated checks if the Codex CLI has stored credentials.
func (c *Client) IsAuthenticated() bool {
	cmd := exec.Command(c.binary, "login", "status")
	cmd.Env = c.env()
	return cmd.Run() == nil
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

// JSONL event types from codex exec --json output.

type jsonlEvent struct {
	Type string          `json:"type"`
	Item json.RawMessage `json:"item,omitempty"`

	// turn.completed fields
	Usage *turnUsage `json:"usage,omitempty"`
}

type itemData struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type turnUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
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

// ChatCompletion runs codex exec and parses the JSONL output.
// workdirOverride, if non-empty, overrides the default working directory.
func (c *Client) ChatCompletion(model string, messages []openai.Message, imageURLs []string, workdirOverride string) (*openai.ChatResponse, error) {
	prompt := buildPrompt(messages)

	// Download images to temp files for --image flags.
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
	// Clean up temp files after exec.
	defer func() {
		for _, p := range imagePaths {
			os.Remove(p)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	args := []string{
		"exec",
		"--json",
		"--full-auto",
		"--skip-git-repo-check",
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	for _, p := range imagePaths {
		args = append(args, "--image", p)
	}

	// Pass prompt via stdin — newer codex CLI versions read from stdin
	// instead of accepting the prompt as a trailing positional argument.
	promptReader := strings.NewReader(prompt)

	cmd := exec.CommandContext(ctx, c.binary, args...)
	if workdirOverride != "" {
		cmd.Dir = workdirOverride
	} else {
		cmd.Dir = c.workdir
	}
	cmd.Env = append(os.Environ(), "CODEX_HOME="+c.codexHome)

	if c.debug {
		log.Printf("[codex-cli] running: %s %s (prompt via stdin, %d bytes)", c.binary, strings.Join(args, " "), len(prompt))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdin = promptReader
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("codex CLI timed out after %s", c.timeout)
		}
		// Try to extract a useful error from the JSONL stdout.
		if errMsg := extractJSONLError(stdout.Bytes()); errMsg != "" {
			return nil, fmt.Errorf("codex CLI: %s", errMsg)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("codex CLI exited with code %d: stderr=%s stdout=%s",
				exitErr.ExitCode(), stderr.String(), truncate(stdout.String(), 500))
		}
		return nil, fmt.Errorf("codex CLI exec: %w", err)
	}

	return parseJSONL(stdout.Bytes(), c.debug)
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

// parseJSONL extracts the final agent message and usage from JSONL output.
func parseJSONL(data []byte, debug bool) (*openai.ChatResponse, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	var responseText string
	var usage openai.Usage

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event jsonlEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			if debug {
				log.Printf("[codex-cli] skip unparseable line: %s", line)
			}
			continue
		}

		switch event.Type {
		case "item.completed":
			var item itemData
			if err := json.Unmarshal(event.Item, &item); err == nil {
				if item.Type == "agent_message" && item.Text != "" {
					responseText = item.Text
				}
			}
		case "turn.completed":
			if event.Usage != nil {
				usage.PromptTokens = event.Usage.InputTokens
				usage.CompletionTokens = event.Usage.OutputTokens
				usage.CachedTokens = event.Usage.CachedInputTokens
				usage.TotalTokens = event.Usage.InputTokens + event.Usage.OutputTokens
			}
		default:
			if debug {
				log.Printf("[codex-cli] event: %s", event.Type)
			}
		}
	}

	if responseText == "" {
		return nil, fmt.Errorf("codex CLI produced no agent_message in output (%d bytes)", len(data))
	}

	return &openai.ChatResponse{
		ID: "codex-cli",
		Choices: []openai.Choice{
			{Message: openai.Message{Role: "assistant", Content: responseText}},
		},
		Usage: usage,
	}, nil
}

// extractJSONLError scans JSONL output for error or turn.failed events.
func extractJSONLError(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var lastErr string
	for scanner.Scan() {
		var event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(scanner.Text()), &event) != nil {
			continue
		}
		if event.Type == "turn.failed" && event.Error.Message != "" {
			return event.Error.Message
		}
		if event.Type == "error" && event.Message != "" {
			lastErr = event.Message
		}
	}
	return lastErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
