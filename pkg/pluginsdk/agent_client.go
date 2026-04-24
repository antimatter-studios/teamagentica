package pluginsdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const agentChatPath = "/chat"

// sseReadTimeout is how long the client waits for any data (including heartbeats)
// before assuming the connection is dead. The server sends heartbeats every 15s,
// so 45s allows for 3 missed heartbeats before giving up.
const sseReadTimeout = 45 * time.Second

// deadlineReader wraps an io.Reader with a per-read deadline using a timer.
// Each successful Read resets the timer. If the timer fires before data arrives,
// the underlying reader's context is cancelled, causing Read to return an error.
type deadlineReader struct {
	r      io.Reader
	cancel context.CancelFunc
	timer  *time.Timer
}

func newDeadlineReader(r io.Reader, cancel context.CancelFunc, timeout time.Duration) *deadlineReader {
	return &deadlineReader{
		r:      r,
		cancel: cancel,
		timer:  time.AfterFunc(timeout, cancel),
	}
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if n > 0 {
		d.timer.Reset(sseReadTimeout)
	}
	return n, err
}

// AgentChat sends a chat request to an agent plugin and collects the final
// response. This is the simple "fire and collect" variant — it consumes
// the SSE stream internally and returns the DoneEvent.
func (c *Client) AgentChat(ctx context.Context, pluginID string, req AgentChatRequest) (*DoneEvent, error) {
	resp, err := c.agentStream(ctx, pluginID, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	scanner := newSSEScanner(resp.Body)
	var result DoneEvent

	for scanner.Scan() {
		line := scanner.Text()
		eventType, data, ok := parseSSELine(line)
		if !ok {
			continue
		}

		switch eventType {
		case "done":
			if err := json.Unmarshal([]byte(data), &result); err != nil {
				return nil, fmt.Errorf("parse done event: %w", err)
			}
		case "error":
			var errEv ErrorEvent
			if err := json.Unmarshal([]byte(data), &errEv); err == nil && errEv.Error != "" {
				return nil, fmt.Errorf("agent error: %s", errEv.Error)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading agent stream: %w", err)
	}

	return &result, nil
}

// AgentChatStream sends a chat request to an agent plugin and returns a
// channel of stream events. The channel is closed when the stream ends.
// The caller should read all events until the channel is closed.
func (c *Client) AgentChatStream(ctx context.Context, pluginID string, req AgentChatRequest) (<-chan AgentStreamEvent, error) {
	// Create a child context so the deadline reader can cancel reads on timeout.
	readCtx, readCancel := context.WithCancel(ctx)

	resp, err := c.agentStream(readCtx, pluginID, req)
	if err != nil {
		readCancel()
		return nil, err
	}

	ch := make(chan AgentStreamEvent, 32)

	go func() {
		defer close(ch)
		defer resp.Body.Close()
		defer readCancel()

		dr := newDeadlineReader(resp.Body, readCancel, sseReadTimeout)
		scanner := newSSEScanner(dr)
		// Track the last event type from "event:" lines.
		var lastEventType string

		for scanner.Scan() {
			line := scanner.Text()

			// Skip heartbeat comments (": heartbeat").
			if strings.HasPrefix(line, ":") {
				continue
			}

			// Track event type lines.
			if strings.HasPrefix(line, "event: ") {
				lastEventType = strings.TrimPrefix(line, "event: ")
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			ev := parseStreamEvent(lastEventType, data)
			if ev.Type != "" {
				ch <- ev
			}
			lastEventType = "" // reset after consuming
		}

		if err := scanner.Err(); err != nil {
			// Don't emit error if the parent context was cancelled (normal shutdown).
			if ctx.Err() == nil {
				ch <- AgentStreamEvent{
					Type: "error",
					Data: ErrorEvent{Error: fmt.Sprintf("reading stream: %v", err)},
				}
			}
		}
	}()

	return ch, nil
}

// agentStream opens a streaming HTTP connection to an agent plugin's /chat endpoint.
func (c *Client) agentStream(ctx context.Context, pluginID string, req AgentChatRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal agent request: %w", err)
	}

	resp, err := c.RouteToPluginStream(ctx, pluginID, "POST", agentChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("stream to %s: %w", pluginID, err)
	}

	return resp, nil
}

// newSSEScanner creates a bufio.Scanner configured for SSE parsing
// with a 256KB line buffer for large tool call arguments. Binary
// attachments (images, video) are delivered by reference as storage:// or
// https:// URLs in AgentAttachment.URL, not inlined in the SSE stream.
func newSSEScanner(r interface{ Read([]byte) (int, error) }) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	return scanner
}

// parseSSELine extracts the event type and data from an SSE "data:" line.
// Returns empty strings if the line isn't a data line or is [DONE].
// This is the simple variant used by AgentChat which only cares about
// done/error events and infers type from the JSON content.
func parseSSELine(line string) (eventType, data string, ok bool) {
	if !strings.HasPrefix(line, "data: ") {
		return "", "", false
	}
	data = strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return "", "", false
	}

	// Infer event type from JSON fields.
	var probe struct {
		Response string `json:"response"`
		Error    string `json:"error"`
		Content  string `json:"content"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal([]byte(data), &probe); err != nil {
		return "", "", false
	}

	switch {
	case probe.Response != "":
		return "done", data, true
	case probe.Error != "" && probe.Name == "":
		return "error", data, true
	case probe.Content != "":
		return "token", data, true
	case probe.Name != "":
		return "tool", data, true
	}

	return "unknown", data, true
}

// parseStreamEvent converts a raw SSE event type + JSON data into an AgentStreamEvent.
func parseStreamEvent(eventType, data string) AgentStreamEvent {
	switch eventType {
	case "token":
		var ev TokenEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			return AgentStreamEvent{Type: "token", Data: ev}
		}
	case "tool_call":
		var ev ToolCallEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			return AgentStreamEvent{Type: "tool_call", Data: ev}
		}
	case "tool_result":
		var ev ToolResultEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			return AgentStreamEvent{Type: "tool_result", Data: ev}
		}
	case "done":
		var ev DoneEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			return AgentStreamEvent{Type: "done", Data: ev}
		}
	case "error":
		var ev ErrorEvent
		if err := json.Unmarshal([]byte(data), &ev); err == nil {
			return AgentStreamEvent{Type: "error", Data: ev}
		}
	default:
		// Fall back to field-sniffing if event type wasn't tracked.
		if eventType == "" {
			return inferStreamEvent(data)
		}
	}
	return AgentStreamEvent{}
}

// inferStreamEvent determines event type from JSON fields when the
// SSE "event:" line wasn't available (e.g. simpler SSE producers).
func inferStreamEvent(data string) AgentStreamEvent {
	var probe struct {
		Content  string `json:"content"`
		Response string `json:"response"`
		Error    string `json:"error"`
		Name     string `json:"name"`
		Result   string `json:"result"`
	}
	if err := json.Unmarshal([]byte(data), &probe); err != nil {
		return AgentStreamEvent{}
	}

	switch {
	case probe.Response != "":
		var ev DoneEvent
		json.Unmarshal([]byte(data), &ev)
		return AgentStreamEvent{Type: "done", Data: ev}
	case probe.Error != "" && probe.Name == "":
		return AgentStreamEvent{Type: "error", Data: ErrorEvent{Error: probe.Error}}
	case probe.Content != "":
		return AgentStreamEvent{Type: "token", Data: TokenEvent{Content: probe.Content}}
	case probe.Name != "" && (probe.Result != "" || probe.Error != ""):
		var ev ToolResultEvent
		json.Unmarshal([]byte(data), &ev)
		return AgentStreamEvent{Type: "tool_result", Data: ev}
	case probe.Name != "":
		var ev ToolCallEvent
		json.Unmarshal([]byte(data), &ev)
		return AgentStreamEvent{Type: "tool_call", Data: ev}
	}

	return AgentStreamEvent{}
}
