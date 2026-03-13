package channels

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// CallbackStore maps short IDs to callback messages for Discord component interactions.
// Thread-safe. Entries expire after 1 hour to prevent unbounded growth.
type CallbackStore struct {
	mu      sync.RWMutex
	entries map[string]callbackEntry
}

type callbackEntry struct {
	Message   string
	CreatedAt time.Time
}

const callbackTTL = 1 * time.Hour

// NewCallbackStore creates a new callback store.
func NewCallbackStore() *CallbackStore {
	cs := &CallbackStore{entries: make(map[string]callbackEntry)}
	go cs.cleanupLoop()
	return cs
}

// Store saves a callback message and returns a short ID for use as a Discord CustomID.
func (cs *CallbackStore) Store(callbackMessage string) string {
	id := shortID()
	cs.mu.Lock()
	cs.entries[id] = callbackEntry{Message: callbackMessage, CreatedAt: time.Now()}
	cs.mu.Unlock()
	return "cb:" + id
}

// Lookup retrieves and removes a callback message by its CustomID.
// Returns the message and true if found, empty string and false otherwise.
func (cs *CallbackStore) Lookup(customID string) (string, bool) {
	if len(customID) < 4 || customID[:3] != "cb:" {
		return "", false
	}
	id := customID[3:]
	cs.mu.Lock()
	entry, ok := cs.entries[id]
	if ok {
		delete(cs.entries, id)
	}
	cs.mu.Unlock()
	if !ok {
		return "", false
	}
	return entry.Message, true
}

func (cs *CallbackStore) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cs.mu.Lock()
		now := time.Now()
		for id, entry := range cs.entries {
			if now.Sub(entry.CreatedAt) > callbackTTL {
				delete(cs.entries, id)
			}
		}
		cs.mu.Unlock()
	}
}

func shortID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
