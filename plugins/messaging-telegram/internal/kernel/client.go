package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	// maxHistory is the max number of message pairs (user+assistant) kept per chat.
	maxHistory = 20
)

// Client communicates with the kernel REST API.
type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
	debug        bool

	// Per-chat conversation history keyed by chat ID.
	histMu  sync.Mutex
	history map[int64][]conversationMsg
}

// chatRequest is the request body for the routed chat endpoint.
type chatRequest struct {
	Message      string            `json:"message"`
	Model        string            `json:"model,omitempty"`
	ImageURLs    []string          `json:"image_urls,omitempty"`
	Conversation []conversationMsg `json:"conversation"`
}

type conversationMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the response body from the chat endpoint.
type chatResponse struct {
	Response string `json:"response"`
}

// NewClient creates a new kernel API client.
func NewClient(baseURL, serviceToken string, debug bool) *Client {
	return &Client{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		debug:        debug,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		history: make(map[int64][]conversationMsg),
	}
}

// ClearHistory resets conversation history for a chat.
func (c *Client) ClearHistory(chatID int64) {
	c.histMu.Lock()
	delete(c.history, chatID)
	c.histMu.Unlock()
}

// ReportEvent sends a debug event to the kernel for display in the console.
func (c *Client) ReportEvent(ctx context.Context, pluginID, eventType, detail string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	payload := map[string]string{
		"id":     pluginID,
		"type":   eventType,
		"detail": detail,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/plugins/event", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// searchResponse is the kernel's response to a plugin capability search.
type searchResponse struct {
	Plugins []pluginInfo `json:"plugins"`
}

type pluginInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// --- Video tool discovery + generation ---

// videoGenerateResponse is the response from POST /generate on a video tool.
type videoGenerateResponse struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

// videoStatusResponse is the response from GET /status/:taskId on a video tool.
type videoStatusResponse struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	VideoURI string `json:"video_uri"`
	VideoURL string `json:"video_url"`
	Error    string `json:"error"`
	Prompt   string `json:"prompt"`
}

// FindVideoTool discovers a video generation plugin via capability search.
// If provider is non-empty, searches for that specific variant (e.g. "tool:video:veo").
func (c *Client) FindVideoTool(ctx context.Context, provider string) (string, error) {
	capability := "tool:video"
	if provider != "" {
		capability = "tool:video:" + provider
	}

	url := fmt.Sprintf("%s/api/plugins/search?capability=%s", c.baseURL, capability)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("searching for video tool: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel search returned status %d: %s", resp.StatusCode, string(body))
	}

	var searchResp searchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("unmarshalling search response: %w", err)
	}

	for _, p := range searchResp.Plugins {
		if p.Status == "running" {
			return p.ID, nil
		}
	}

	return "", fmt.Errorf("no video tool installed (searched for %s)", capability)
}

// GenerateVideo submits a video generation request to a video tool plugin.
func (c *Client) GenerateVideo(ctx context.Context, provider, prompt string) (*videoGenerateResponse, error) {
	pluginID, err := c.FindVideoTool(ctx, provider)
	if err != nil {
		return nil, err
	}

	reqBody := map[string]string{"prompt": prompt}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/generate", c.baseURL, pluginID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending generate request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("generate returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp videoGenerateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	return &genResp, nil
}

// CheckVideoStatus polls the status of a video generation task.
func (c *Client) CheckVideoStatus(ctx context.Context, provider, taskID string) (*videoStatusResponse, error) {
	pluginID, err := c.FindVideoTool(ctx, provider)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/route/%s/status/%s", c.baseURL, pluginID, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending status request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status returned %d: %s", resp.StatusCode, string(body))
	}

	var statusResp videoStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return nil, fmt.Errorf("unmarshalling status: %w", err)
	}

	return &statusResp, nil
}

// --- Image tool discovery + generation ---

// imageGenerateResponse is the response from POST /generate on an image tool.
type imageGenerateResponse struct {
	Status    string `json:"status"`
	ImageData string `json:"image_data"`
	MimeType  string `json:"mime_type"`
	Seed      string `json:"seed"`
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	Error     string `json:"error"`
}

// FindImageTool discovers an image generation plugin via capability search.
// If provider is non-empty, searches for that specific variant (e.g. "tool:image:stability").
func (c *Client) FindImageTool(ctx context.Context, provider string) (string, error) {
	capability := "tool:image"
	if provider != "" {
		capability = "tool:image:" + provider
	}

	url := fmt.Sprintf("%s/api/plugins/search?capability=%s", c.baseURL, capability)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("searching for image tool: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel search returned status %d: %s", resp.StatusCode, string(body))
	}

	var searchResp searchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("unmarshalling search response: %w", err)
	}

	for _, p := range searchResp.Plugins {
		if p.Status == "running" {
			return p.ID, nil
		}
	}

	return "", fmt.Errorf("no image tool installed (searched for %s)", capability)
}

// GenerateImage submits an image generation request to an image tool plugin.
// Synchronous — the response contains the base64 image data directly.
func (c *Client) GenerateImage(ctx context.Context, provider, prompt string) (*imageGenerateResponse, error) {
	pluginID, err := c.FindImageTool(ctx, provider)
	if err != nil {
		return nil, err
	}

	reqBody := map[string]string{"prompt": prompt}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/generate", c.baseURL, pluginID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending generate request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("generate returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp imageGenerateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	if genResp.Error != "" {
		return nil, fmt.Errorf("image generation failed: %s", genResp.Error)
	}

	return &genResp, nil
}

// ChatWithAgentDirect routes a message to a specific plugin+model.
// Includes per-chat conversation history.
func (c *Client) ChatWithAgentDirect(ctx context.Context, chatID int64, userID int64, pluginID, model, message string, imageURLs []string, systemPrompt string) (string, error) {
	return c.chatWithPlugin(ctx, chatID, userID, pluginID, model, message, imageURLs, systemPrompt)
}

// chatWithPlugin is the shared HTTP+history logic for routing a chat message.
func (c *Client) chatWithPlugin(ctx context.Context, chatID int64, userID int64, pluginID, model, message string, imageURLs []string, systemPrompt string) (string, error) {
	// Build conversation: optional system prompt + prior history + new user message.
	c.histMu.Lock()
	hist := c.history[chatID]
	conv := make([]conversationMsg, 0, len(hist)+2)
	if systemPrompt != "" {
		conv = append(conv, conversationMsg{Role: "system", Content: systemPrompt})
	}
	conv = append(conv, hist...)
	c.histMu.Unlock()

	conv = append(conv, conversationMsg{Role: "user", Content: message})

	reqBody := chatRequest{
		Message:      message,
		Model:        model,
		ImageURLs:    imageURLs,
		Conversation: conv,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/chat", c.baseURL, pluginID)

	if c.debug {
		log.Printf("[kernel] POST %s", url)
		log.Printf("[kernel] request: agent=%s model=%q history=%d message=%q",
			pluginID, model, len(conv)-1, truncateStr(message, 200))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	if userID != 0 {
		req.Header.Set("X-Teamagentica-User-ID", fmt.Sprintf("telegram:%d", userID))
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[kernel] request failed after %v: %v", elapsed, err)
		return "", fmt.Errorf("sending request to kernel: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %w", err)
	}

	if c.debug {
		log.Printf("[kernel] response status=%d time=%v body=%s",
			resp.StatusCode, elapsed, truncateStr(string(respBody), 500))
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshalling response: %w", err)
	}

	if c.debug {
		log.Printf("[kernel] agent response: %s", truncateStr(chatResp.Response, 300))
	}

	// Store the exchange in history (without the system prompt).
	c.histMu.Lock()
	c.history[chatID] = append(c.history[chatID],
		conversationMsg{Role: "user", Content: message},
		conversationMsg{Role: "assistant", Content: chatResp.Response},
	)
	if len(c.history[chatID]) > maxHistory*2 {
		c.history[chatID] = c.history[chatID][len(c.history[chatID])-maxHistory*2:]
	}
	c.histMu.Unlock()

	return chatResp.Response, nil
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
