package agentkit

import (
	"context"
	"encoding/json"
)

// ProviderAdapter is the only thing each agent plugin needs to implement.
// Everything else (routing, tools, streaming, health, schema) is handled by the kit.
type ProviderAdapter interface {
	// StreamChat sends a chat request to the LLM and streams responses via the sink.
	// The adapter calls sink methods as data arrives from the provider API.
	// When the LLM requests tool use, return FinishReasonToolUse so the runtime
	// can execute tools and call StreamChat again with the results appended.
	StreamChat(ctx context.Context, req ProviderRequest, sink EventSink) (ProviderResult, error)

	// ModelID returns the default model identifier (e.g. "claude-sonnet-4-20250514").
	ModelID() string

	// ProviderName returns the provider name for usage tracking (e.g. "anthropic", "openai").
	ProviderName() string
}

// ProviderRequest contains everything the provider needs to call its LLM API.
type ProviderRequest struct {
	Messages     []Message
	SystemPrompt string
	Tools        []ToolDefinition
	Model        string  // override from request, or empty for default
	MaxTokens    int
	Temperature  float64
	WorkspaceID  string  // workspace context, if any
	SessionID    string  // session/conversation ID
}

// Message is a provider-agnostic chat message.
type Message struct {
	Role       string      // "user", "assistant", "system", "tool"
	Content    string
	ToolCalls  []ToolCall  // assistant requesting tool use (role="assistant")
	ToolResult *ToolResult // result of a tool call (role="tool")
}

// ToolCall represents the LLM requesting a tool execution.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult is the response from executing a tool call.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// EventSink receives streaming events from the provider adapter.
// The runtime provides an implementation that forwards to SSE or channels.
type EventSink interface {
	// SendText emits a text content delta.
	SendText(text string) error

	// SendToolCall emits a tool use request from the LLM.
	SendToolCall(call ToolCall) error

	// SendDone signals the stream is complete.
	SendDone() error

	// SendError signals a stream error.
	SendError(err error) error
}

// ProviderResult is returned after StreamChat completes.
type ProviderResult struct {
	FinishReason string // "end_turn", "tool_use", "max_tokens", "stop"
	ToolCalls    []ToolCall
	Usage        Usage
	CostUSD      float64 // provider-reported cost, if available
	NumTurns     int     // number of turns in the session
	SessionID    string  // provider session ID, if applicable
}

// Finish reason constants.
const (
	FinishReasonEndTurn   = "end_turn"
	FinishReasonToolUse   = "tool_use"
	FinishReasonMaxTokens = "max_tokens"
	FinishReasonStop      = "stop"
)

// Usage holds token counts from a single LLM call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
}
