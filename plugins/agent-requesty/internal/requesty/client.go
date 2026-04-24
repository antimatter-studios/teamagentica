package requesty

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://router.requesty.ai/v1"

// Message is the standard role+content pair used by the handler layer.
// ImageURLs (if any) trigger OpenAI-style multipart vision content.
type Message struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ImageURLs  []string        `json:"-"`
	ToolCalls  []ToolCallDelta `json:"tool_calls,omitempty"`   // assistant tool-use requests
	ToolCallID string          `json:"tool_call_id,omitempty"` // tool result reference
}

// buildAPIMessages converts Messages into the OpenAI API format.
// Messages with ImageURLs emit multipart content arrays for vision.
func buildAPIMessages(messages []Message) []interface{} {
	result := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			result = append(result, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      msg.Content,
			})
			continue
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			m := map[string]interface{}{
				"role":       "assistant",
				"tool_calls": msg.ToolCalls,
			}
			if msg.Content != "" {
				m["content"] = msg.Content
			}
			result = append(result, m)
			continue
		}

		if len(msg.ImageURLs) > 0 {
			content := []map[string]interface{}{
				{"type": "text", "text": msg.Content},
			}
			for _, u := range msg.ImageURLs {
				content = append(content, map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]string{"url": u},
				})
			}
			result = append(result, map[string]interface{}{
				"role":    msg.Role,
				"content": content,
			})
			continue
		}

		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return result
}

// Usage holds token counts from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Client talks to the Requesty AI router (OpenAI-compatible).
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

// ListModels returns available models from the Requesty API.
func (c *Client) ListModels() ([]string, error) {
	url := fmt.Sprintf("%s/models", baseURL)

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}

	return models, nil
}
