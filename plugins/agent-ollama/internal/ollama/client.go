package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Message represents a single chat message (OpenAI-compatible format).
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

// Usage tracks token usage for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

var httpClient = &http.Client{
	Timeout: 300 * time.Second,
}

// buildAPIMessages converts Messages into the OpenAI API format.
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

// ── Streaming ───────────────────────────────────────────────────────────────────

// StreamEvent represents a single event from the streaming API.
type StreamEvent struct {
	Token        string
	ToolCalls    []StreamToolCallDelta
	FinishReason string
	Usage        *Usage
	Err          error
}

// StreamToolCallDelta represents a partial tool call from a streaming chunk.
type StreamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type streamChunk struct {
	ID      string         `json:"id"`
	Choices []streamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []StreamToolCallDelta `json:"tool_calls,omitempty"`
}

// ChatCompletionStream opens a streaming connection to the Ollama API.
func ChatCompletionStream(ctx context.Context, endpoint, model string, messages []Message, tools []ToolDef) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		reqBody := map[string]interface{}{
			"model":    model,
			"messages": buildAPIMessages(messages),
			"stream":   true,
		}
		if len(tools) > 0 {
			reqBody["tools"] = tools
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("marshal request: %w", err)}
			return
		}

		url := fmt.Sprintf("%s/v1/chat/completions", endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("build request: %w", err)}
			return
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("http request: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var errBody [4096]byte
			n, _ := resp.Body.Read(errBody[:])
			ch <- StreamEvent{Err: fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(errBody[:n]))}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			var chunk streamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				ch <- StreamEvent{Err: fmt.Errorf("unmarshal chunk: %w", err)}
				return
			}

			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					ch <- StreamEvent{Usage: chunk.Usage}
				}
				continue
			}

			choice := chunk.Choices[0]
			ev := StreamEvent{
				Token:     choice.Delta.Content,
				ToolCalls: choice.Delta.ToolCalls,
			}
			if choice.FinishReason != nil {
				ev.FinishReason = *choice.FinishReason
			}
			if chunk.Usage != nil {
				ev.Usage = chunk.Usage
			}
			ch <- ev
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("reading stream: %w", err)}
		}
	}()

	return ch
}

// AccumulateToolCalls assembles fragmented tool call deltas into complete ToolCalls.
func AccumulateToolCalls(accumulated []ToolCall, deltas []StreamToolCallDelta) []ToolCall {
	for _, d := range deltas {
		for len(accumulated) <= d.Index {
			accumulated = append(accumulated, ToolCall{})
		}
		tc := &accumulated[d.Index]
		if d.ID != "" {
			tc.ID = d.ID
		}
		if d.Type != "" {
			tc.Type = d.Type
		}
		if d.Function.Name != "" {
			tc.Function.Name = d.Function.Name
		}
		tc.Function.Arguments += d.Function.Arguments
	}
	return accumulated
}

// ── Model management ────────────────────────────────────────────────────────────

// ListModels returns available model names from Ollama.
func ListModels(endpoint string) ([]string, error) {
	resp, err := httpClient.Get(endpoint + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	sort.Strings(names)
	return names, nil
}

// PullModel pulls a model from the Ollama registry. Blocks until complete.
func PullModel(endpoint, model string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"name":   model,
		"stream": false,
	})
	resp, err := httpClient.Post(endpoint+"/api/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pull model %s: %w", model, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull model %s: status %d", model, resp.StatusCode)
	}
	return nil
}

// DeleteModel removes a model from Ollama.
func DeleteModel(endpoint, model string) error {
	body, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequest(http.MethodDelete, endpoint+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("delete model %s: %w", model, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete model %s: %w", model, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete model %s: status %d", model, resp.StatusCode)
	}
	return nil
}

// Healthy checks if Ollama is reachable.
func Healthy(endpoint string) error {
	resp, err := httpClient.Get(endpoint + "/api/tags")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ollama unhealthy: status %d", resp.StatusCode)
	}
	return nil
}
