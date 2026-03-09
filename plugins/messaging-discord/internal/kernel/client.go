package kernel

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client communicates with the kernel REST API.
type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

// chatRequest is the request body for the routed chat endpoint.
type chatRequest struct {
	Message       string            `json:"message"`
	Model         string            `json:"model,omitempty"`
	ImageURLs     []string          `json:"image_urls,omitempty"`
	Conversation  []conversationMsg `json:"conversation"`
	IsCoordinator bool              `json:"is_coordinator,omitempty"`
	AgentAlias    string            `json:"agent_alias,omitempty"`
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

// searchResponse is the kernel's response to a plugin capability search.
type searchResponse struct {
	Plugins []pluginInfo `json:"plugins"`
}

type pluginInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
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
func (c *Client) FindImageTool(provider string) (string, error) {
	capability := "tool:image"
	if provider != "" {
		capability = "tool:image:" + provider
	}
	return c.findPluginByCapability(capability)
}

// GenerateImage submits an image generation request to an image tool plugin.
func (c *Client) GenerateImage(provider, prompt string) (*imageGenerateResponse, error) {
	pluginID, err := c.FindImageTool(provider)
	if err != nil {
		return nil, err
	}

	reqBody := map[string]string{"prompt": prompt}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/generate", c.baseURL, pluginID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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

// --- Video tool discovery + generation ---

// videoGenerateResponse is the response from POST /generate on a video tool.
type videoGenerateResponse struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

// VideoStatusResponse is the response from GET /status/:taskId on a video tool.
type VideoStatusResponse struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	VideoURI string `json:"video_uri"`
	VideoURL string `json:"video_url"`
	Error    string `json:"error"`
	Prompt   string `json:"prompt"`
}

// FindVideoTool discovers a video generation plugin via capability search.
func (c *Client) FindVideoTool(provider string) (string, error) {
	capability := "tool:video"
	if provider != "" {
		capability = "tool:video:" + provider
	}
	return c.findPluginByCapability(capability)
}

// GenerateVideo submits a video generation request to a video tool plugin.
func (c *Client) GenerateVideo(provider, prompt string) (*videoGenerateResponse, error) {
	pluginID, err := c.FindVideoTool(provider)
	if err != nil {
		return nil, err
	}

	reqBody := map[string]string{"prompt": prompt}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/generate", c.baseURL, pluginID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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
func (c *Client) CheckVideoStatus(provider, taskID string) (*VideoStatusResponse, error) {
	pluginID, err := c.FindVideoTool(provider)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/route/%s/status/%s", c.baseURL, pluginID, taskID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

	var statusResp VideoStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return nil, fmt.Errorf("unmarshalling status: %w", err)
	}

	return &statusResp, nil
}

// findPluginByCapability searches the kernel for a running plugin with the given capability.
func (c *Client) findPluginByCapability(capability string) (string, error) {
	url := fmt.Sprintf("%s/api/plugins/search?capability=%s", c.baseURL, capability)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("searching for plugin: %w", err)
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

	return "", fmt.Errorf("no plugin found with capability %s", capability)
}

// ChatWithAgentDirect routes a message to a specific plugin.
// Pass isCoordinator=true for coordinator calls, and agentAlias for the target agent's alias.
func (c *Client) ChatWithAgentDirect(pluginID, model, message string, imageURLs []string, isCoordinator bool, agentAlias string) (string, error) {
	return c.chatWithPlugin(pluginID, model, message, imageURLs, isCoordinator, agentAlias)
}

// chatWithPlugin is the shared HTTP logic for routing a chat message to a plugin.
func (c *Client) chatWithPlugin(pluginID, model, message string, imageURLs []string, isCoordinator bool, agentAlias string) (string, error) {
	conv := []conversationMsg{{Role: "user", Content: message}}

	reqBody := chatRequest{
		Message:       message,
		Model:         model,
		ImageURLs:     imageURLs,
		Conversation:  conv,
		IsCoordinator: isCoordinator,
		AgentAlias:    agentAlias,
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
