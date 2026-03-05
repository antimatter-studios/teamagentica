package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EventType distinguishes one-shot from repeating events.
type EventType string

const (
	Once   EventType = "once"
	Repeat EventType = "repeat"
)

// Event is a scheduled task that fires at a specific time or on an interval.
type Event struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Text       string        `json:"text"`
	Type       EventType     `json:"type"`
	Interval   time.Duration `json:"interval_ns"`
	IntervalHR string        `json:"interval"` // human-readable, e.g. "5m", "1h"
	NextFire   time.Time     `json:"next_fire"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Enabled    bool          `json:"enabled"`
	FiredCount int           `json:"fired_count"`
}

// LogEntry records a single firing of an event.
type LogEntry struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	EventName string    `json:"event_name"`
	Text      string    `json:"text"`
	FiredAt   time.Time `json:"fired_at"`
}

// Scheduler manages scheduled events and fires them when due.
type Scheduler struct {
	mu     sync.RWMutex
	events map[string]*Event
	logMu  sync.RWMutex
	logs   []LogEntry
	stopCh chan struct{}
}

// New creates and starts a new Scheduler.
func New() *Scheduler {
	s := &Scheduler{
		events: make(map[string]*Event),
		stopCh: make(chan struct{}),
	}
	go s.run()
	return s
}

// Stop halts the scheduler loop.
func (s *Scheduler) Stop() {
	close(s.stopCh)
}

// CreateEvent adds a new scheduled event.
// timeout is how long from now until first firing.
func CreateEvent(name, text string, eventType EventType, interval time.Duration) *Event {
	now := time.Now()
	hr := formatDuration(interval)
	return &Event{
		ID:         uuid.New().String(),
		Name:       name,
		Text:       text,
		Type:       eventType,
		Interval:   interval,
		IntervalHR: hr,
		NextFire:   now.Add(interval),
		CreatedAt:  now,
		UpdatedAt:  now,
		Enabled:    true,
	}
}

// Add inserts an event into the scheduler.
func (s *Scheduler) Add(e *Event) {
	s.mu.Lock()
	s.events[e.ID] = e
	s.mu.Unlock()
	log.Printf("[scheduler] added event %s (%s) type=%s interval=%s next_fire=%s",
		e.ID, e.Name, e.Type, e.IntervalHR, e.NextFire.Format(time.RFC3339))
}

// Get returns a single event by ID.
func (s *Scheduler) Get(id string) (*Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.events[id]
	if !ok {
		return nil, false
	}
	cp := *e
	return &cp, true
}

// List returns all events.
func (s *Scheduler) List() []*Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Event, 0, len(s.events))
	for _, e := range s.events {
		cp := *e
		result = append(result, &cp)
	}
	return result
}

// Update modifies an existing event. Returns error if not found.
func (s *Scheduler) Update(id string, name, text *string, interval *time.Duration, enabled *bool) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.events[id]
	if !ok {
		return nil, fmt.Errorf("event %s not found", id)
	}

	if name != nil {
		e.Name = *name
	}
	if text != nil {
		e.Text = *text
	}
	if interval != nil {
		e.Interval = *interval
		e.IntervalHR = formatDuration(*interval)
		e.NextFire = time.Now().Add(*interval)
	}
	if enabled != nil {
		e.Enabled = *enabled
		if *enabled && e.NextFire.Before(time.Now()) {
			// Re-arm if re-enabling a past event.
			e.NextFire = time.Now().Add(e.Interval)
		}
	}
	e.UpdatedAt = time.Now()

	cp := *e
	return &cp, nil
}

// Delete removes an event. Returns false if not found.
func (s *Scheduler) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.events[id]; !ok {
		return false
	}
	delete(s.events, id)
	return true
}

// Logs returns the event firing log, newest first.
func (s *Scheduler) Logs(limit int) []LogEntry {
	s.logMu.RLock()
	defer s.logMu.RUnlock()

	n := len(s.logs)
	if limit <= 0 || limit > n {
		limit = n
	}

	// Return newest first.
	result := make([]LogEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = s.logs[n-1-i]
	}
	return result
}

// run is the scheduler tick loop. Checks every second for due events.
func (s *Scheduler) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// tick checks all events and fires any that are due.
func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	var toFire []*Event
	for _, e := range s.events {
		if !e.Enabled {
			continue
		}
		if now.After(e.NextFire) || now.Equal(e.NextFire) {
			toFire = append(toFire, e)
		}
	}

	// Process firings while holding lock to update state atomically.
	for _, e := range toFire {
		e.FiredCount++
		log.Printf("[scheduler] FIRED event %s (%s): %s", e.ID, e.Name, e.Text)

		switch e.Type {
		case Once:
			e.Enabled = false
		case Repeat:
			e.NextFire = now.Add(e.Interval)
		}
	}
	s.mu.Unlock()

	// Append log entries outside the events lock.
	if len(toFire) > 0 {
		s.logMu.Lock()
		for _, e := range toFire {
			s.logs = append(s.logs, LogEntry{
				ID:        uuid.New().String(),
				EventID:   e.ID,
				EventName: e.Name,
				Text:      e.Text,
				FiredAt:   now,
			})
		}
		// Cap log at 1000 entries.
		if len(s.logs) > 1000 {
			s.logs = s.logs[len(s.logs)-1000:]
		}
		s.logMu.Unlock()
	}
}

func formatDuration(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return d.String()
}
