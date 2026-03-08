package inception

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Message represents a single chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// ChatRequest is the request body for the Inception chat completions API.
type ChatRequest struct {
	Model           string        `json:"model"`
	Messages        []interface{} `json:"messages"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
	Diffusing       bool          `json:"diffusing,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"` // "instant", "low", "medium", "high"
	Tools           []ToolDef     `json:"tools,omitempty"`
	ToolChoice      interface{}   `json:"tool_choice,omitempty"`
	ResponseFormat  *ResponseFmt  `json:"response_format,omitempty"`
}

// ResponseFmt controls structured output.
type ResponseFmt struct {
	Type       string          `json:"type"`                  // "json_schema"
	JSONSchema json.RawMessage `json:"json_schema,omitempty"` // {"name":..., "strict":..., "schema":...}
}

// FIMRequest is the request body for the FIM (fill-in-the-middle) endpoint.
type FIMRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Suffix string `json:"suffix"`
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
}

// ChatResponse is the response from the Inception chat completions API.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

// buildAPIMessages converts Messages into the OpenAI-compatible API format.
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

		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return result
}

// ChatCompletion calls the Inception chat completions API.
func ChatCompletion(apiKey, endpoint, model string, messages []Message, opts ...RequestOption) (*ChatResponse, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(o)
	}

	reqBody := ChatRequest{
		Model:    model,
		Messages: buildAPIMessages(messages),
	}
	if o.maxTokens > 0 {
		reqBody.MaxTokens = o.maxTokens
	}
	if o.temperature != nil {
		reqBody.Temperature = o.temperature
	}
	if o.diffusing {
		reqBody.Diffusing = true
	}
	if o.reasoningEffort != "" {
		reqBody.ReasoningEffort = o.reasoningEffort
	}
	if len(o.tools) > 0 {
		reqBody.Tools = o.tools
		reqBody.ToolChoice = "auto"
	}
	if o.responseFormat != nil {
		reqBody.ResponseFormat = o.responseFormat
	}

	return doRequest(apiKey, fmt.Sprintf("%s/chat/completions", endpoint), reqBody)
}

// ApplyEdit calls POST /v1/apply/completions — merges update snippet into original code.
func ApplyEdit(apiKey, endpoint, originalCode, updateSnippet string) (*ChatResponse, error) {
	content := fmt.Sprintf("<|original_code|>\n%s\n<|/original_code|>\n\n<|update_snippet|>\n%s\n<|/update_snippet|>", originalCode, updateSnippet)

	reqBody := ChatRequest{
		Model: "mercury-edit",
		Messages: []interface{}{
			map[string]interface{}{"role": "user", "content": content},
		},
		MaxTokens: 4096,
	}

	return doRequest(apiKey, fmt.Sprintf("%s/apply/completions", endpoint), reqBody)
}

// NextEdit calls POST /v1/edit/completions — predicts next code edit based on context.
func NextEdit(apiKey, endpoint, recentSnippets, currentFileContent, editDiffHistory string) (*ChatResponse, error) {
	content := fmt.Sprintf(
		"<|recently_viewed_code_snippets|>\n%s\n<|/recently_viewed_code_snippets|>\n\n<|current_file_content|>\n%s\n<|/current_file_content|>\n\n<|edit_diff_history|>\n%s\n<|/edit_diff_history|>",
		recentSnippets, currentFileContent, editDiffHistory,
	)

	reqBody := ChatRequest{
		Model: "mercury-edit",
		Messages: []interface{}{
			map[string]interface{}{"role": "user", "content": content},
		},
	}

	return doRequest(apiKey, fmt.Sprintf("%s/edit/completions", endpoint), reqBody)
}

// FIMCompletion calls POST /v1/fim/completions — fill-in-the-middle autocomplete.
func FIMCompletion(apiKey, endpoint, prompt, suffix string) (*ChatResponse, error) {
	reqBody := FIMRequest{
		Model:  "mercury-edit",
		Prompt: prompt,
		Suffix: suffix,
	}

	return doRequest(apiKey, fmt.Sprintf("%s/fim/completions", endpoint), reqBody)
}

// doRequest marshals body, sends POST, and parses ChatResponse.
func doRequest(apiKey, url string, reqBody interface{}) (*ChatResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		return nil, fmt.Errorf("Inception API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

// RequestOption configures a ChatCompletion call.
type RequestOption func(*requestOptions)

type requestOptions struct {
	maxTokens       int
	temperature     *float64
	diffusing       bool
	reasoningEffort string
	tools           []ToolDef
	responseFormat  *ResponseFmt
}

func defaultOptions() *requestOptions {
	return &requestOptions{}
}

func WithMaxTokens(n int) RequestOption {
	return func(o *requestOptions) { o.maxTokens = n }
}

func WithTemperature(t float64) RequestOption {
	return func(o *requestOptions) { o.temperature = &t }
}

func WithDiffusing(enabled bool) RequestOption {
	return func(o *requestOptions) { o.diffusing = enabled }
}

func WithReasoningEffort(effort string) RequestOption {
	return func(o *requestOptions) { o.reasoningEffort = effort }
}

func WithTools(tools []ToolDef) RequestOption {
	return func(o *requestOptions) { o.tools = tools }
}

func WithResponseFormat(fmt *ResponseFmt) RequestOption {
	return func(o *requestOptions) { o.responseFormat = fmt }
}

// ListModels calls GET /v1/models and returns available model IDs.
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
		return fallbackModels, nil
	}

	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &models); err != nil {
		return fallbackModels, nil
	}

	var result []string
	for _, m := range models.Data {
		result = append(result, m.ID)
	}
	if len(result) == 0 {
		return fallbackModels, nil
	}
	return result, nil
}

var fallbackModels = []string{
	"mercury-2",
	"mercury-coder-small",
	"mercury-edit",
}
