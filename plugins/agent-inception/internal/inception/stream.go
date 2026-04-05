package inception

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

type streamRequest struct {
	Model           string        `json:"model"`
	Messages        []interface{} `json:"messages"`
	Tools           []ToolDef     `json:"tools,omitempty"`
	ToolChoice      interface{}   `json:"tool_choice,omitempty"`
	Stream          bool          `json:"stream"`
	StreamOptions   *streamOpts   `json:"stream_options,omitempty"`
	Diffusing       bool          `json:"diffusing,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionStream opens a streaming connection to the Inception chat completions API
// and sends events on the returned channel.
func ChatCompletionStream(ctx context.Context, apiKey, endpoint, model string, messages []Message, tools []ToolDef, opts ...RequestOption) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	go func() {
		defer close(ch)

		o := defaultOptions()
		for _, fn := range opts {
			fn(o)
		}

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
			reqBody.ToolChoice = "auto"
		}
		if o.diffusing {
			reqBody.Diffusing = true
		}
		if o.reasoningEffort != "" {
			reqBody.ReasoningEffort = o.reasoningEffort
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
			ch <- StreamEvent{Err: fmt.Errorf("Inception returned status %d: %s", resp.StatusCode, string(errBody[:n]))}
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
