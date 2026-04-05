package pluginsdk

import "context"

// AgentChatRequest is the standard request sent to any agent plugin's chat endpoint.
// All agent plugins accept this same structure.
type AgentChatRequest struct {
	Message      string            `json:"message"`
	Model        string            `json:"model,omitempty"`
	ImageURLs    []string          `json:"image_urls,omitempty"`
	Conversation []ConversationMsg `json:"conversation"`
	AgentAlias   string            `json:"agent_alias,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`

	// UserID is populated by the SDK from the X-Teamagentica-User-ID header.
	// Providers use this for usage tracking. Not sent over JSON.
	UserID string `json:"-"`

	// Provider-specific fields (e.g. reasoning_effort, diffusing).
	// Agents that don't recognise these ignore them.
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// ConversationMsg is a single message in the conversation history.
type ConversationMsg struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCalls  interface{} `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

// AgentStreamEvent is one event emitted during a streaming chat response.
type AgentStreamEvent struct {
	Type string      // "token", "tool_call", "tool_result", "done", "error"
	Data interface{} // One of the *Event types below.
}

// TokenEvent is emitted for each text content delta.
type TokenEvent struct {
	Content string `json:"content"`
}

// ToolCallEvent is emitted when the model invokes a tool.
type ToolCallEvent struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResultEvent is emitted after a tool finishes executing.
type ToolResultEvent struct {
	Name   string `json:"name"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// DoneEvent is the final event containing the complete response.
type DoneEvent struct {
	Response    string             `json:"response"`
	Model       string             `json:"model,omitempty"`
	Backend     string             `json:"backend,omitempty"`
	Usage       *AgentUsage        `json:"usage,omitempty"`
	Attachments []AgentAttachment  `json:"attachments,omitempty"`
}

// ErrorEvent is emitted when the stream encounters an error.
type ErrorEvent struct {
	Error string `json:"error"`
}

// AgentUsage holds token counts from a chat completion.
type AgentUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

// AgentAttachment represents media attached to a response.
type AgentAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data,omitempty"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// AgentProvider is the interface that agent plugins implement to handle
// streaming chat completions. The SDK handles SSE framing, route registration,
// and request parsing — providers just produce events.
type AgentProvider interface {
	ChatStream(ctx context.Context, req AgentChatRequest) <-chan AgentStreamEvent
}
