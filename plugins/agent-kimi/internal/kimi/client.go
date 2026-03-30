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
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// Usage holds token counts from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is the parsed result of a chat completion call.
type ChatResponse struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	Usage            Usage
	Headers          http.Header
}

// ToolDef describes a tool available for function calling.
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
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatCompletionRequest is the request body for /v1/chat/completions.
type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []interface{} `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []ToolDef     `json:"tools,omitempty"`
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

// buildAPIMessages converts typed Messages into interface{} maps suitable for
// the JSON request body, handling tool call and tool result messages correctly.
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
			if msg.ReasoningContent != "" {
				m["reasoning_content"] = msg.ReasoningContent
			}
			result = append(result, m)
			continue
		}
		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return result
}

// ChatCompletion sends a chat request to the Kimi chat/completions endpoint.
func (c *Client) ChatCompletion(model string, messages []Message, tools ...ToolDef) (*ChatResponse, error) {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: buildAPIMessages(messages),
		Stream:   false,
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
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
				Content          string     `json:"content"`
				ReasoningContent string     `json:"reasoning_content,omitempty"`
				ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
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
	reasoningText := ""
	finishReason := ""
	var toolCalls []ToolCall
	if len(result.Choices) > 0 {
		responseText = result.Choices[0].Message.Content
		reasoningText = result.Choices[0].Message.ReasoningContent
		toolCalls = result.Choices[0].Message.ToolCalls
		finishReason = result.Choices[0].FinishReason
	}

	return &ChatResponse{
		Content:          responseText,
		ReasoningContent: reasoningText,
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage: Usage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
		Headers: resp.Header,
	}, nil
}

// ListModels returns available models from the Kimi API.
// On any failure it returns nil and an error — no fallback lists.
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
		name := strings.ToLower(m.ID)
		if strings.Contains(name, "kimi") || strings.Contains(name, "moonshot") {
			models = append(models, m.ID)
		}
	}

	return models, nil
}
