package kimi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://api.moonshot.ai/v1"

// Message is the standard role+content pair used by the handler layer.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage holds token counts from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is the parsed result of a chat completion call.
type ChatResponse struct {
	Content string
	Usage   Usage
	Headers http.Header
}

// chatCompletionRequest is the request body for /v1/chat/completions.
type chatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// Client talks to the Moonshot Kimi API (OpenAI-compatible).
type Client struct {
	apiKey     string
	httpClient *http.Client
	debug      bool
}

func NewClient(apiKey string, debug bool) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		debug: debug,
	}
}

// ChatCompletion sends a chat request to the Kimi chat/completions endpoint.
func (c *Client) ChatCompletion(model string, messages []Message) (*ChatResponse, error) {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", baseURL)

	if c.debug {
		log.Printf("[kimi] POST %s body=%s", url, string(jsonBody))
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[kimi] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kimi API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	responseText := ""
	if len(result.Choices) > 0 {
		responseText = result.Choices[0].Message.Content
	}

	return &ChatResponse{
		Content: responseText,
		Usage: Usage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
		Headers: resp.Header,
	}, nil
}

// ListModels returns available models from the Kimi API.
func (c *Client) ListModels() ([]string, bool, error) {
	url := fmt.Sprintf("%s/models", baseURL)

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return DefaultModels(), true, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return DefaultModels(), true, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return DefaultModels(), true, fmt.Errorf("list models returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return DefaultModels(), true, fmt.Errorf("parse models: %w", err)
	}

	var models []string
	for _, m := range result.Data {
		name := strings.ToLower(m.ID)
		if strings.Contains(name, "kimi") || strings.Contains(name, "moonshot") {
			models = append(models, m.ID)
		}
	}

	if len(models) == 0 {
		return DefaultModels(), true, nil
	}

	return models, false, nil
}

func DefaultModels() []string {
	return []string{
		"kimi-k2-turbo-preview",
		"kimi-k2.5",
		"kimi-k2-0905-preview",
		"kimi-k2-0711-preview",
		"kimi-k2-thinking-turbo",
		"kimi-k2-thinking",
		"moonshot-v1-128k",
		"moonshot-v1-32k",
		"moonshot-v1-8k",
		"moonshot-v1-auto",
	}
}
