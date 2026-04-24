package requesty

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
	Usage        *Usage
	ToolCalls    []ToolCallDelta // accumulated tool call deltas
	Err          error
}

// ToolCallDelta is a tool call from the streaming response.
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// ToolDef is a tool definition in the OpenAI format.
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes the function within a tool definition.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
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
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// StreamOptions configures the ChatCompletionStream call.
type StreamOptions struct {
	Tools       []ToolDef
	MaxTokens   int
	Temperature *float64
}

// ChatCompletionStream opens a streaming connection and sends events on the returned channel.
func ChatCompletionStream(ctx context.Context, apiKey, model string, messages []Message, opts *StreamOptions) <-chan StreamEvent {
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
		if opts != nil {
			if len(opts.Tools) > 0 {
				reqBody.Tools = opts.Tools
			}
			if opts.MaxTokens > 0 {
				reqBody.MaxTokens = opts.MaxTokens
			}
			if opts.Temperature != nil {
				reqBody.Temperature = opts.Temperature
			}
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
			ch <- StreamEvent{Err: fmt.Errorf("Requesty returned status %d: %s", resp.StatusCode, string(errBody[:n]))}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

		// Accumulate tool call deltas across chunks (OpenAI streams them incrementally).
		toolCallAccum := map[int]*ToolCallDelta{}

		for scanner.Scan() {
			line := scanner.Text()

			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			if data == "[DONE]" {
				break
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

			// Accumulate tool call deltas.
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallAccum[tc.Index]
				if !ok {
					copy := tc
					toolCallAccum[tc.Index] = &copy
				} else {
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
					existing.Function.Arguments += tc.Function.Arguments
				}
			}

			ev := StreamEvent{
				Token: choice.Delta.Content,
			}
			if choice.FinishReason != nil {
				ev.FinishReason = *choice.FinishReason
			}
			if chunk.Usage != nil {
				ev.Usage = chunk.Usage
			}

			// On finish with tool_calls, emit accumulated tool calls.
			if ev.FinishReason == "tool_calls" {
				var calls []ToolCallDelta
				for i := 0; i < len(toolCallAccum); i++ {
					if tc, ok := toolCallAccum[i]; ok {
						calls = append(calls, *tc)
					}
				}
				ev.ToolCalls = calls
			}

			ch <- ev
		}

		// If we exited without a finish_reason but have accumulated tool calls, emit them.
		if len(toolCallAccum) > 0 {
			var calls []ToolCallDelta
			for i := 0; i < len(toolCallAccum); i++ {
				if tc, ok := toolCallAccum[i]; ok {
					calls = append(calls, *tc)
				}
			}
			if len(calls) > 0 {
				ch <- StreamEvent{FinishReason: "tool_calls", ToolCalls: calls}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("reading stream: %w", err)}
		}
	}()

	return ch
}
