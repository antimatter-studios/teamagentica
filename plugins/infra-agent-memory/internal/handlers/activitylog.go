package handlers

import (
	"fmt"
	"sync"
	"time"
)

// ActivityType classifies a memory operation for display.
type ActivityType string

const (
	ActivityRecall  ActivityType = "recall"
	ActivitySave    ActivityType = "save"
	ActivityUpdate  ActivityType = "update"
	ActivityDelete  ActivityType = "delete"
	ActivityCompact ActivityType = "compact"
	ActivityFlush   ActivityType = "flush"
)

// ActivityEntry is a single memory operation recorded in the activity log.
type ActivityEntry struct {
	Time      string       `json:"time"`
	Type      ActivityType `json:"type"`
	Direction string       `json:"direction"` // "in" or "out"
	SessionID string       `json:"session_id,omitempty"`
	Agent     string       `json:"agent,omitempty"`
	Summary   string       `json:"summary"`
	Details   string       `json:"details,omitempty"`
}

// ActivityLog is a thread-safe ring buffer of recent memory operations.
type ActivityLog struct {
	mu      sync.RWMutex
	entries []ActivityEntry
	maxSize int
}

// NewActivityLog creates a ring buffer with the given capacity.
func NewActivityLog(maxSize int) *ActivityLog {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &ActivityLog{
		entries: make([]ActivityEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Record adds an entry to the activity log.
func (l *ActivityLog) Record(typ ActivityType, direction, sessionID, agent, summary, details string) {
	entry := ActivityEntry{
		Time:      time.Now().Format("3:04:05 PM"),
		Type:      typ,
		Direction: direction,
		SessionID: sessionID,
		Agent:     agent,
		Summary:   summary,
		Details:   details,
	}

	l.mu.Lock()
	l.entries = append(l.entries, entry)
	if len(l.entries) > l.maxSize {
		// Drop oldest entries.
		l.entries = l.entries[len(l.entries)-l.maxSize:]
	}
	l.mu.Unlock()
}

// Entries returns all entries newest-first for schema display.
func (l *ActivityLog) Entries() []ActivityEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Return reversed copy (newest first).
	result := make([]ActivityEntry, len(l.entries))
	for i, e := range l.entries {
		result[len(l.entries)-1-i] = e
	}
	return result
}

// Helpers for common recording patterns.

func (l *ActivityLog) RecordRecall(query, sessionID string, resultCount int) {
	l.Record(ActivityRecall, "in", sessionID, "",
		fmt.Sprintf("recall: %q → %d results", truncate(query, 60), resultCount), "")
}

func (l *ActivityLog) RecordSave(category, content, agent, sessionID string) {
	l.Record(ActivitySave, "in", sessionID, agent,
		fmt.Sprintf("save [%s]: %s", category, truncate(content, 80)), "")
}

func (l *ActivityLog) RecordUpdate(id uint, sessionID string) {
	l.Record(ActivityUpdate, "in", sessionID, "",
		fmt.Sprintf("update memory #%d", id), "")
}

func (l *ActivityLog) RecordDelete(id uint) {
	l.Record(ActivityDelete, "in", "", "",
		fmt.Sprintf("delete memory #%d", id), "")
}

func (l *ActivityLog) RecordCompactDispatch(sessionID string, msgCount int) {
	l.Record(ActivityCompact, "out", sessionID, "",
		fmt.Sprintf("compact dispatched: %d messages", msgCount), "")
}

func (l *ActivityLog) RecordCompactResult(sessionID string, factCount int) {
	l.Record(ActivityCompact, "in", sessionID, "",
		fmt.Sprintf("compact complete: %d facts extracted", factCount), "")
}

func (l *ActivityLog) RecordFlush(sessionID string, msgCount int, trigger string) {
	l.Record(ActivityFlush, "out", sessionID, "",
		fmt.Sprintf("buffer flush: %d messages (%s)", msgCount, trigger), "")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
