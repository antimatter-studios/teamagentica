package kimi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// StreamEvent represents a single event from the streaming API.
type StreamEvent struct {
	Token            string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	Usage            *Usage
	Err              error
}

// AccumulateToolCalls merges incremental tool call deltas into a running slice.
func AccumulateToolCalls(existing []ToolCall, deltas []ToolCall) []ToolCall {
	for _, d := range deltas {
		if d.ID != "" {
			// New tool call.
			existing = append(existing, d)
		} else if len(existing) > 0 {
			// Append arguments to the last tool call.
			last := &existing[len(existing)-1]
			last.Function.Arguments += d.Function.Arguments
		}
	}
	return existing
}

// ChatCompletionStream opens a streaming chat completion to the Kimi API.
// Returns a channel that emits StreamEvents. The channel is closed when done.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, messages []Message, tools ...ToolDef) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)

		reqBody := chatCompletionRequest{
			Model:    model,
			Messages: buildAPIMessages(messages),
			Stream:   true,
		}
		if len(tools) > 0 {
			reqBody.Tools = tools
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("marshal request: %w", err)}
			return
		}

		url := fmt.Sprintf("%s/chat/completions", baseURL)

		if c.debug {
			log.Printf("[kimi] POST %s (stream) body=%s", url, string(jsonBody))
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("create request: %w", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		// Use a client without timeout for streaming — context handles cancellation.
		streamClient := &http.Client{}
		resp, err := streamClient.Do(httpReq)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("http request: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var buf bytes.Buffer
			buf.ReadFrom(resp.Body)
			ch <- StreamEvent{Err: fmt.Errorf("kimi API returned %d: %s", resp.StatusCode, buf.String())}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content          string     `json:"content"`
						ReasoningContent string     `json:"reasoning_content,omitempty"`
						ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				if c.debug {
					log.Printf("[kimi] failed to parse stream chunk: %v data=%s", err, data)
				}
				continue
			}

			ev := StreamEvent{}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				if choice.Delta.Content != "" {
					ev.Token = choice.Delta.Content
				}
				if choice.Delta.ReasoningContent != "" {
					ev.ReasoningContent = choice.Delta.ReasoningContent
				}
				if len(choice.Delta.ToolCalls) > 0 {
					ev.ToolCalls = choice.Delta.ToolCalls
				}
				if choice.FinishReason != nil {
					ev.FinishReason = *choice.FinishReason
				}
			}

			if chunk.Usage != nil {
				ev.Usage = &Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}

			if ev.Token != "" || ev.ReasoningContent != "" || len(ev.ToolCalls) > 0 || ev.FinishReason != "" || ev.Usage != nil {
				ch <- ev
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("read stream: %w", err)}
		}
	}()

	return ch
}
