package anthropic

import (
	"encoding/json"
)

// ToolDef describes a tool available for function calling.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

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

