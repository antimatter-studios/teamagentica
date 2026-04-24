package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// StreamEvent represents a single event from the streaming API.
type StreamEvent struct {
	Token        string
	FinishReason string
	ToolCalls    []ToolCallDelta
	Usage        *Usage
	Err          error
}

// ToolCallDelta represents a tool call chunk from the streaming API.
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// ToolDef describes a tool for the OpenAI-compatible API.
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function descriptor inside a ToolDef.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCallFunction holds the function name and arguments in a tool call message.
// This is distinct from ToolFunction (which holds the schema for tool definitions).
type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallMessage is a tool_call entry in an assistant message.
type ToolCallMessage struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// streamChunk is the raw JSON structure of a streaming SSE chunk.
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
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
}

type streamRequest struct {
	Model         string        `json:"model"`
	Messages      []interface{} `json:"messages"`
	Tools         []ToolDef     `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *streamOpts   `json:"stream_options,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionStream opens a streaming connection and sends events on the returned channel.
func ChatCompletionStream(ctx context.Context, apiKey, model string, messages []Message, tools []ToolDef) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		reqBody := streamRequest{
			Model:    model,
			Messages: buildAPIMessages(messages),
			Stream:   true,
			StreamOptions: &streamOpts{
				IncludeUsage: true,
			},
		}
		if len(tools) > 0 {
			reqBody.Tools = tools
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("marshal request: %w", err)}
			return
		}

		url := fmt.Sprintf("%s/chat/completions", baseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("build request: %w", err)}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://teamagentica.dev")
		req.Header.Set("X-Title", "TeamAgentica")

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
			ch <- StreamEvent{Err: fmt.Errorf("OpenRouter returned status %d: %s", resp.StatusCode, string(errBody[:n]))}
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
				Token: choice.Delta.Content,
			}
			if len(choice.Delta.ToolCalls) > 0 {
				ev.ToolCalls = choice.Delta.ToolCalls
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
