package kernel

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client communicates with the kernel REST API.
type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client

	// Cached AI agent discovery.
	mu              sync.Mutex
	cachedAgentID   string
	cachedAgentTime time.Time
}

const agentCacheTTL = 60 * time.Second

// searchResponse is the response body from the plugin search endpoint.
type searchResponse struct {
	Plugins []pluginInfo `json:"plugins"`
}

type pluginInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// chatRequest is the request body for the routed chat endpoint.
type chatRequest struct {
	Message      string            `json:"message"`
	Model        string            `json:"model,omitempty"`
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
// If tlsConfig is non-nil, the client uses it for mTLS connections.
func NewClient(baseURL, serviceToken string, tlsConfig *tls.Config) *Client {
	httpClient := &http.Client{
		Timeout: 120 * time.Second,
	}
	if tlsConfig != nil {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}
	return &Client{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient:   httpClient,
	}
}

// FindAIAgent discovers an AI chat plugin via the kernel's capability search.
// Results are cached for 60 seconds.
func (c *Client) FindAIAgent() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Return cached result if still valid.
	if c.cachedAgentID != "" && time.Since(c.cachedAgentTime) < agentCacheTTL {
		return c.cachedAgentID, nil
	}

	url := fmt.Sprintf("%s/api/plugins/search?capability=ai:chat", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating search request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("searching for AI agent: %w", err)
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

	// Find first running plugin.
	for _, p := range searchResp.Plugins {
		if p.Status == "running" {
			c.cachedAgentID = p.ID
			c.cachedAgentTime = time.Now()
			return p.ID, nil
		}
	}

	return "", fmt.Errorf("no AI agent installed")
}

// ChatWithAgent discovers an AI agent and routes the message through the kernel proxy.
func (c *Client) ChatWithAgent(message string) (string, error) {
	pluginID, err := c.FindAIAgent()
	if err != nil {
		return "", fmt.Errorf("finding AI agent: %w", err)
	}

	return c.chatWithPlugin(pluginID, "", message, "")
}

// ChatWithAgentDirect routes a message to a specific plugin, bypassing discovery.
// Accepts an optional systemPrompt prepended to the conversation.
func (c *Client) ChatWithAgentDirect(pluginID, model, message, systemPrompt string) (string, error) {
	return c.chatWithPlugin(pluginID, model, message, systemPrompt)
}

// chatWithPlugin is the shared HTTP logic for routing a chat message to a plugin.
func (c *Client) chatWithPlugin(pluginID, model, message, systemPrompt string) (string, error) {
	var conv []conversationMsg
	if systemPrompt != "" {
		conv = append(conv, conversationMsg{Role: "system", Content: systemPrompt})
	}
	conv = append(conv, conversationMsg{Role: "user", Content: message})

	reqBody := chatRequest{
		Message:      message,
		Model:        model,
		Conversation: conv,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/chat", c.baseURL, pluginID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request to kernel: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshalling response: %w", err)
	}

	return chatResp.Response, nil
}
