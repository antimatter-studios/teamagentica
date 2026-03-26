package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// StreamEvent represents a single event from the OpenAI streaming API.
type StreamEvent struct {
	// Token contains a text content delta (may be empty).
	Token string

	// ToolCalls contains tool call deltas being assembled.
	// Each entry has an Index to identify which tool call it belongs to.
	ToolCalls []StreamToolCallDelta

	// FinishReason is set on the final chunk ("stop" or "tool_calls").
	FinishReason string

	// Usage is populated on the final chunk (if the API returns it).
	Usage *Usage

	// Err is set if an error occurred during streaming.
	Err error
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

// streamChunk is the raw JSON structure of a streaming SSE chunk from OpenAI.
type streamChunk struct {
	ID      string         `json:"id"`
	Choices []streamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// streamChoice is a single choice in a streaming chunk.
type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// streamDelta is the delta content in a streaming chunk.
type streamDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []StreamToolCallDelta `json:"tool_calls,omitempty"`
}

// streamRequest extends ChatRequest with stream flag.
type streamRequest struct {
	Model         string        `json:"model"`
	Messages      []interface{} `json:"messages"`
	Tools         []ToolDef     `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *streamOpts   `json:"stream_options,omitempty"`
}

// streamOpts configures streaming behaviour.
type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionStream opens a streaming connection to the OpenAI chat completions API
// and sends events on the returned channel. The channel is closed when the stream ends.
// The caller should read all events until the channel is closed.
func ChatCompletionStream(ctx context.Context, apiKey, endpoint, model string, messages []Message, tools []ToolDef) <-chan StreamEvent {
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

		url := fmt.Sprintf("%s/chat/completions", endpoint)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("build request: %w", err)}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		// Use a client without timeout — streaming can run long.
		// Context cancellation handles cleanup.
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("http request: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			// Read error body for diagnostics.
			var errBody [4096]byte
			n, _ := resp.Body.Read(errBody[:])
			ch <- StreamEvent{Err: fmt.Errorf("OpenAI returned status %d: %s", resp.StatusCode, string(errBody[:n]))}
			return
		}

		// Parse SSE stream line by line.
		scanner := bufio.NewScanner(resp.Body)
		// Allow up to 256KB per line for large tool call arguments.
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

		for scanner.Scan() {
			line := scanner.Text()

			// SSE format: "data: {...}" or "data: [DONE]"
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
				// Usage-only chunk (final) — emit usage and continue.
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
// OpenAI streams tool calls in pieces: first chunk has ID+name, subsequent chunks
// append to arguments. This function merges them by index.
func AccumulateToolCalls(accumulated []ToolCall, deltas []StreamToolCallDelta) []ToolCall {
	for _, d := range deltas {
		// Grow slice if needed.
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
