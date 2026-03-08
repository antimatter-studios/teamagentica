package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const maxHistory = 20

// Client communicates with the kernel REST API.
type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
	debug        bool

	histMu  sync.Mutex
	history map[string][]conversationMsg
}

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

type chatResponse struct {
	Response string `json:"response"`
}

// NewClient creates a new kernel API client.
func NewClient(baseURL, serviceToken string, debug bool) *Client {
	return &Client{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		debug:        debug,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
		history:      make(map[string][]conversationMsg),
	}
}

// ClearHistory resets conversation history for a chat.
func (c *Client) ClearHistory(chatID string) {
	c.histMu.Lock()
	delete(c.history, chatID)
	c.histMu.Unlock()
}

// ChatWithAgentDirect routes a message to a specific plugin+model.
// Includes per-chat conversation history.
func (c *Client) ChatWithAgentDirect(chatID, pluginID, model, message string, imageURLs []string, systemPrompt string) (string, error) {
	return c.chatWithPlugin(chatID, pluginID, model, message, imageURLs, systemPrompt)
}

// chatWithPlugin is the shared HTTP+history logic for routing a chat message.
func (c *Client) chatWithPlugin(chatID, pluginID, model, message string, imageURLs []string, systemPrompt string) (string, error) {
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
		log.Printf("[kernel] POST %s agent=%s model=%q history=%d",
			url, pluginID, model, len(conv)-1)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel returned %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshalling response: %w", err)
	}

	// Store exchange in history (without the system prompt).
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
