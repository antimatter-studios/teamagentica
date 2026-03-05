package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const maxHistory = 500

// DebugEvent represents a single event in the debug stream.
type DebugEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // "proxy", "register", "deregister", "heartbeat", "install", "error", etc.
	PluginID  string    `json:"plugin_id"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path,omitempty"`
	Status    int       `json:"status,omitempty"`
	Duration  int64     `json:"duration_ms,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// SSEMessage wraps a typed payload for the multiplexed SSE stream.
// Channel is "audit" for DebugEvents or "event" for EventLog entries.
type SSEMessage struct {
	Channel string `json:"channel"`
	Data    any    `json:"data"`
}

// Hub is a fan-out event broadcaster with an in-memory event log.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan SSEMessage]struct{}
	history []SSEMessage
}

// NewHub creates a new event Hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan SSEMessage]struct{}),
		history: make([]SSEMessage, 0, maxHistory),
	}
}

// Subscribe returns a channel that receives SSE messages. Call Unsubscribe when done.
func (h *Hub) Subscribe() chan SSEMessage {
	ch := make(chan SSEMessage, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel and closes it.
func (h *Hub) Unsubscribe(ch chan SSEMessage) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// Emit sends an audit event to all connected clients and appends it to history.
func (h *Hub) Emit(evt DebugEvent) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	h.send(SSEMessage{Channel: "audit", Data: evt})
}

// EmitEvent sends an inter-plugin event log entry to all connected clients.
func (h *Hub) EmitEvent(entry any) {
	h.send(SSEMessage{Channel: "event", Data: entry})
}

// send broadcasts an SSEMessage to all clients and appends to history.
func (h *Hub) send(msg SSEMessage) {
	h.mu.Lock()
	if len(h.history) >= maxHistory {
		h.history = h.history[1:]
	}
	h.history = append(h.history, msg)
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	h.mu.Unlock()
}

// History returns the most recent SSE messages (up to limit). Pass 0 for all.
func (h *Hub) History(limit int) []SSEMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if limit <= 0 || limit > len(h.history) {
		limit = len(h.history)
	}
	start := len(h.history) - limit
	out := make([]SSEMessage, limit)
	copy(out, h.history[start:])
	return out
}

// MarshalSSEMessage serializes an SSEMessage to SSE wire format: "event: <channel>\ndata: <json>\n\n".
func MarshalSSEMessage(msg SSEMessage) ([]byte, error) {
	data, err := json.Marshal(msg.Data)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", msg.Channel, data)), nil
}
