package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// Message represents a single chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ImageURLs  []string   `json:"image_urls,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolDef describes a tool available for OpenAI function calling.
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a callable function.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall represents a tool invocation requested by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// ChatRequest is the request body for the OpenAI chat completions API.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []interface{} `json:"messages"`
	Tools    []ToolDef     `json:"tools,omitempty"`
}

// Choice is one completion choice in a chat response.
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage tracks token usage for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// ChatResponse is the response from the OpenAI chat completions API.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

// buildAPIMessages converts Messages into the OpenAI API format.
// Messages with ImageURLs use the content array format for vision.
// Messages with ToolCalls or ToolCallID are formatted for the tool-use protocol.
func buildAPIMessages(messages []Message) []interface{} {
	result := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		// Tool result message.
		if msg.Role == "tool" && msg.ToolCallID != "" {
			result = append(result, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      msg.Content,
			})
			continue
		}

		// Assistant message with tool calls (no text content expected).
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
			// Build multipart content array.
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
		} else {
			result = append(result, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}
	return result
}

// modelsResponse is the response from the OpenAI /v1/models endpoint.
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels calls GET /v1/models and returns all available model IDs.
func ListModels(apiKey, endpoint string) ([]string, error) {
	url := fmt.Sprintf("%s/models", endpoint)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI returned status %d: %s", resp.StatusCode, string(body))
	}

	var models modelsResponse
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	result := make([]string, len(models.Data))
	for i, m := range models.Data {
		result[i] = m.ID
	}
	sort.Strings(result)
	return result, nil
}
