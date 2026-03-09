package bot

import (
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

// MessageBuffer accumulates messages per channel and flushes them after a
// debounce window of inactivity.  Thread-safe.
type MessageBuffer struct {
	mu       sync.Mutex
	chats    map[string]*chatBuffer
	duration time.Duration
	onFlush  func(channelID string, text string, mediaURLs []string)
}

// NewMessageBuffer creates a buffer that calls onFlush when a channel goes
// quiet for the given duration.
func NewMessageBuffer(duration time.Duration, onFlush func(channelID string, text string, mediaURLs []string)) *MessageBuffer {
	return &MessageBuffer{
		chats:    make(map[string]*chatBuffer),
		duration: duration,
		onFlush:  onFlush,
	}
}

// Add appends a message to the channel's buffer and resets the debounce timer.
func (mb *MessageBuffer) Add(channelID string, text string, mediaURLs []string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	buf, ok := mb.chats[channelID]
	if !ok {
		buf = &chatBuffer{}
		mb.chats[channelID] = buf
	}

	buf.messages = append(buf.messages, bufferedMessage{text: text, mediaURLs: mediaURLs})

	// Reset the timer.
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(mb.duration, func() {
		mb.flush(channelID)
	})
}

// flush merges buffered messages and calls onFlush.
func (mb *MessageBuffer) flush(channelID string) {
	mb.mu.Lock()
	buf, ok := mb.chats[channelID]
	if !ok {
		mb.mu.Unlock()
		return
	}
	messages := buf.messages
	delete(mb.chats, channelID)
	mb.mu.Unlock()

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

	mergedText := ""
	for i, t := range texts {
		if i > 0 {
			mergedText += "\n"
		}
		mergedText += t
	}

	if mergedText == "" && len(allMedia) == 0 {
		return
	}

	mb.onFlush(channelID, mergedText, allMedia)
}

// SetDuration updates the debounce duration for future messages.
func (mb *MessageBuffer) SetDuration(d time.Duration) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.duration = d
}

// Stop cancels all pending timers and flushes remaining messages immediately.
func (mb *MessageBuffer) Stop() {
	mb.mu.Lock()
	channelIDs := make([]string, 0, len(mb.chats))
	for id, buf := range mb.chats {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		channelIDs = append(channelIDs, id)
	}
	mb.mu.Unlock()

	// Flush all pending channels.
	for _, id := range channelIDs {
		mb.flush(id)
	}
}
