package msgbuffer

import (
	"strings"
	"sync"
	"time"
)

// bufferedMessage holds text and media from a single incoming message.
type bufferedMessage struct {
	text      string
	mediaURLs []string
}

// chatBuffer holds accumulated messages and the debounce timer for one channel.
type chatBuffer struct {
	messages []bufferedMessage
	timer    *time.Timer
}

// Buffer accumulates messages per channel and flushes them after a
// debounce window of inactivity. Thread-safe.
//
// Channel IDs are strings — callers with numeric IDs (e.g. Telegram int64)
// should convert with fmt.Sprintf or strconv before calling.
type Buffer struct {
	mu       sync.Mutex
	chats    map[string]*chatBuffer
	duration time.Duration
	onFlush  func(channelID string, text string, mediaURLs []string)
}

// New creates a buffer that calls onFlush when a channel goes
// quiet for the given duration.
func New(duration time.Duration, onFlush func(channelID string, text string, mediaURLs []string)) *Buffer {
	return &Buffer{
		chats:    make(map[string]*chatBuffer),
		duration: duration,
		onFlush:  onFlush,
	}
}

// Add appends a message to the channel's buffer and resets the debounce timer.
func (b *Buffer) Add(channelID string, text string, mediaURLs []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	buf, ok := b.chats[channelID]
	if !ok {
		buf = &chatBuffer{}
		b.chats[channelID] = buf
	}

	buf.messages = append(buf.messages, bufferedMessage{text: text, mediaURLs: mediaURLs})

	// Reset the timer.
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(b.duration, func() {
		b.flush(channelID)
	})
}

// flush merges buffered messages and calls onFlush.
func (b *Buffer) flush(channelID string) {
	b.mu.Lock()
	buf, ok := b.chats[channelID]
	if !ok {
		b.mu.Unlock()
		return
	}
	messages := buf.messages
	delete(b.chats, channelID)
	b.mu.Unlock()

	// Merge texts (newline-joined) and deduplicate media URLs.
	var texts []string
	seen := make(map[string]bool)
	var allMedia []string

	for _, m := range messages {
		if m.text != "" {
			texts = append(texts, m.text)
		}
		for _, url := range m.mediaURLs {
			if !seen[url] {
				seen[url] = true
				allMedia = append(allMedia, url)
			}
		}
	}

	mergedText := strings.Join(texts, "\n")

	if mergedText == "" && len(allMedia) == 0 {
		return
	}

	b.onFlush(channelID, mergedText, allMedia)
}

// SetDuration updates the debounce duration for future messages.
func (b *Buffer) SetDuration(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.duration = d
}

// Stop cancels all pending timers and flushes remaining messages immediately.
func (b *Buffer) Stop() {
	b.mu.Lock()
	channelIDs := make([]string, 0, len(b.chats))
	for id, buf := range b.chats {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		channelIDs = append(channelIDs, id)
	}
	b.mu.Unlock()

	for _, id := range channelIDs {
		b.flush(id)
	}
}
