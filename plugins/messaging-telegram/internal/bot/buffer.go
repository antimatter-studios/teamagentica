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

// chatBuffer holds accumulated messages and the debounce timer for one chat.
type chatBuffer struct {
	messages []bufferedMessage
	timer    *time.Timer
}

// MessageBuffer accumulates messages per chat and flushes them after a
// debounce window of inactivity.  Thread-safe.
type MessageBuffer struct {
	mu       sync.Mutex
	chats    map[int64]*chatBuffer
	duration time.Duration
	onFlush  func(chatID int64, text string, mediaURLs []string)
}

// NewMessageBuffer creates a buffer that calls onFlush when a chat goes
// quiet for the given duration.
func NewMessageBuffer(duration time.Duration, onFlush func(chatID int64, text string, mediaURLs []string)) *MessageBuffer {
	return &MessageBuffer{
		chats:    make(map[int64]*chatBuffer),
		duration: duration,
		onFlush:  onFlush,
	}
}

// Add appends a message to the chat's buffer and resets the debounce timer.
func (mb *MessageBuffer) Add(chatID int64, text string, mediaURLs []string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	buf, ok := mb.chats[chatID]
	if !ok {
		buf = &chatBuffer{}
		mb.chats[chatID] = buf
	}

	buf.messages = append(buf.messages, bufferedMessage{text: text, mediaURLs: mediaURLs})

	// Reset the timer.
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(mb.duration, func() {
		mb.flush(chatID)
	})
}

// flush merges buffered messages and calls onFlush.
func (mb *MessageBuffer) flush(chatID int64) {
	mb.mu.Lock()
	buf, ok := mb.chats[chatID]
	if !ok {
		mb.mu.Unlock()
		return
	}
	messages := buf.messages
	delete(mb.chats, chatID)
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

	mb.onFlush(chatID, mergedText, allMedia)
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
	chatIDs := make([]int64, 0, len(mb.chats))
	for id, buf := range mb.chats {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		chatIDs = append(chatIDs, id)
	}
	mb.mu.Unlock()

	// Flush all pending chats.
	for _, id := range chatIDs {
		mb.flush(id)
	}
}
