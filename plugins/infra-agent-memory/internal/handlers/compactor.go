package handlers

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// bufferedMessage is a single message held in the compaction buffer.
type bufferedMessage struct {
	Role      string
	Content   string
	Responder string
	Timestamp time.Time
}

// sessionBuffer tracks pending messages for a single session.
type sessionBuffer struct {
	messages       []bufferedMessage
	lastCompaction time.Time
	agentAlias     string // most recent responder seen
}

// Compactor manages per-session message buffers and triggers compaction
// when either a message count threshold or a time interval is reached.
type Compactor struct {
	mu       sync.Mutex
	sessions map[string]*sessionBuffer
	handler  *Handler

	msgThreshold int           // compact after this many messages
	interval     time.Duration // compact after this much time
	stop         chan struct{}
}

// NewCompactor creates a compactor that flushes on whichever trigger fires first.
func NewCompactor(h *Handler, msgThreshold int, interval time.Duration) *Compactor {
	if msgThreshold <= 0 {
		msgThreshold = 100
	}
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &Compactor{
		sessions:     make(map[string]*sessionBuffer),
		handler:      h,
		msgThreshold: msgThreshold,
		interval:     interval,
		stop:         make(chan struct{}),
	}
}

// Start launches the background sweep goroutine for time-based compaction.
func (c *Compactor) Start() {
	go c.sweepLoop()
}

// Stop signals the sweep goroutine to exit.
func (c *Compactor) Stop() {
	close(c.stop)
}

// Push adds a message to the session buffer. If the message threshold is
// reached, compaction is triggered immediately in a background goroutine.
func (c *Compactor) Push(sessionID, role, content, responder string) {
	c.mu.Lock()

	buf, ok := c.sessions[sessionID]
	if !ok {
		buf = &sessionBuffer{
			lastCompaction: time.Now(),
		}
		c.sessions[sessionID] = buf
	}

	buf.messages = append(buf.messages, bufferedMessage{
		Role:      role,
		Content:   content,
		Responder: responder,
		Timestamp: time.Now(),
	})
	if responder != "" {
		buf.agentAlias = responder
	}

	// Check message threshold.
	shouldFlush := len(buf.messages) >= c.msgThreshold
	c.mu.Unlock()

	if shouldFlush {
		go c.flush(sessionID)
	}
}

// sweepLoop runs periodically and flushes any session whose buffer has
// exceeded the time interval since its last compaction.
func (c *Compactor) sweepLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.sweepOnce()
		}
	}
}

// sweepOnce checks all sessions and flushes those past the time interval.
func (c *Compactor) sweepOnce() {
	c.mu.Lock()
	var toFlush []string
	now := time.Now()
	for id, buf := range c.sessions {
		if len(buf.messages) > 0 && now.Sub(buf.lastCompaction) >= c.interval {
			toFlush = append(toFlush, id)
		}
	}
	c.mu.Unlock()

	for _, id := range toFlush {
		c.flush(id)
	}
}

// flush extracts the buffered messages for a session, formats them as a
// transcript, and sends them to the compact endpoint for fact extraction.
func (c *Compactor) flush(sessionID string) {
	c.mu.Lock()
	buf, ok := c.sessions[sessionID]
	if !ok || len(buf.messages) == 0 {
		c.mu.Unlock()
		return
	}

	// Drain the buffer.
	messages := buf.messages
	agentAlias := buf.agentAlias
	buf.messages = nil
	buf.lastCompaction = time.Now()
	c.mu.Unlock()

	// Format as a readable transcript.
	transcript := formatTranscript(messages)

	// Determine trigger reason.
	trigger := "message_threshold"
	if len(messages) < c.msgThreshold {
		trigger = "time_interval"
	}

	log.Printf("[compactor] flushing %d messages for session=%s trigger=%s", len(messages), sessionID, trigger)

	if c.handler.activity != nil {
		c.handler.activity.RecordFlush(sessionID, len(messages), trigger)
	}

	// Call the handler's compact logic directly (no HTTP round-trip).
	c.handler.compactTranscript(sessionID, transcript, agentAlias)
}

// FlushAll flushes all sessions with buffered messages. Called on shutdown.
func (c *Compactor) FlushAll() {
	c.mu.Lock()
	var toFlush []string
	for id, buf := range c.sessions {
		if len(buf.messages) > 0 {
			toFlush = append(toFlush, id)
		}
	}
	c.mu.Unlock()

	for _, id := range toFlush {
		c.flush(id)
	}
}

// formatTranscript converts buffered messages into a human-readable transcript.
func formatTranscript(messages []bufferedMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		speaker := m.Role
		if m.Role == "assistant" && m.Responder != "" {
			speaker = fmt.Sprintf("assistant (%s)", m.Responder)
		}
		fmt.Fprintf(&sb, "[%s] %s:\n%s\n\n", m.Timestamp.Format("15:04"), speaker, m.Content)
	}
	return sb.String()
}
