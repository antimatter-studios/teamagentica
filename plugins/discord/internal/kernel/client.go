package kernel

import (
	"bytes"
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

// chatRequest is the request body for the chat endpoint.
type chatRequest struct {
	Message  string `json:"message"`
	ConfigID *uint  `json:"config_id,omitempty"`
}

// chatResponse is the response body from the chat endpoint.
type chatResponse struct {
	Response string `json:"response"`
}

// NewClient creates a new kernel API client.
func NewClient(baseURL, serviceToken string) *Client {
	return &Client{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ChatWithAgent sends a message to the kernel's agent chat endpoint and returns the response.
func (c *Client) ChatWithAgent(message string, configID *uint) (string, error) {
	reqBody := chatRequest{
		Message:  message,
		ConfigID: configID,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/agents/chat", c.baseURL)
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
