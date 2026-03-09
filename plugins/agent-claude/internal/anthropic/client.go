package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ToolDef describes a tool available for function calling.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

const defaultEndpoint = "https://api.anthropic.com/v1"

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolUseBlocks is set when the assistant response includes tool calls.
	ToolUseBlocks []ContentBlock `json:"-"`
	// ToolResults is set when sending tool results back.
	ToolResults []ToolResult `json:"-"`
}

// ToolResult is a tool result content block sent as part of a user message.
type ToolResult struct {
	Type      string `json:"type"`        // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// Usage tracks token usage for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens"`
	CacheCreate  int `json:"cache_creation_input_tokens"`
}

// ContentBlock is a block within the response.
type ContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`    // for tool_use
	Name  string                 `json:"name,omitempty"`  // for tool_use
	Input map[string]interface{} `json:"input,omitempty"` // for tool_use
}

// ChatResponse is the response from the Anthropic messages API.
type ChatResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

// chatRequest is the request body for the Anthropic messages API.
type chatRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Messages  []interface{} `json:"messages"`
	System    string        `json:"system,omitempty"`
	Tools     []ToolDef     `json:"tools,omitempty"`
}

// ChatCompletion calls the Anthropic messages API directly.
// Optional tools can be passed to enable function calling.
func ChatCompletion(apiKey, model string, messages []Message, maxTokens int, tools ...ToolDef) (*ChatResponse, error) {
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	reqBody := chatRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  buildAPIMessages(messages),
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := defaultEndpoint + "/messages"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// GetResponseText extracts the text content from a response.
func GetResponseText(resp *ChatResponse) string {
	for _, block := range resp.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// GetToolUseBlocks returns tool_use content blocks from the response.
func GetToolUseBlocks(resp *ChatResponse) []ContentBlock {
	var blocks []ContentBlock
	for _, b := range resp.Content {
		if b.Type == "tool_use" {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// buildAPIMessages converts Message slices into the interface{} format
// required by the Anthropic API, handling structured content for tool use
// and tool results.
func buildAPIMessages(messages []Message) []interface{} {
	result := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		// Assistant message with tool use blocks.
		if msg.Role == "assistant" && len(msg.ToolUseBlocks) > 0 {
			blocks := make([]interface{}, 0)
			if msg.Content != "" {
				blocks = append(blocks, map[string]interface{}{
					"type": "text",
					"text": msg.Content,
				})
			}
			for _, tb := range msg.ToolUseBlocks {
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tb.ID,
					"name":  tb.Name,
					"input": tb.Input,
				})
			}
			result = append(result, map[string]interface{}{
				"role":    "assistant",
				"content": blocks,
			})
			continue
		}

		// User message with tool results.
		if msg.Role == "user" && len(msg.ToolResults) > 0 {
			blocks := make([]interface{}, 0)
			for _, tr := range msg.ToolResults {
				blocks = append(blocks, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": tr.ToolUseID,
					"content":     tr.Content,
				})
			}
			result = append(result, map[string]interface{}{
				"role":    "user",
				"content": blocks,
			})
			continue
		}

		// Regular message.
		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return result
}
