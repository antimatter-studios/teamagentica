package pluginsdk

import (
	"sync"
	"sync/atomic"
	"time"
)

// Debouncer controls how events are delivered to handlers.
// Supply NullDebouncer for immediate delivery or TimedDebouncer for
// coalesced delivery after a quiet period.
type Debouncer interface {
	// Submit delivers a new event. The debouncer decides when to
	// forward it to the underlying handler.
	Submit(event EventCallback)
	// Stop cleans up any timers. Must be called on shutdown.
	Stop()
}

// --- NullDebouncer: fires every event immediately ---

type nullDebouncer struct {
	handler EventHandler
}

// NewNullDebouncer creates a debouncer that fires every event immediately.
func NewNullDebouncer(handler EventHandler) Debouncer {
	return &nullDebouncer{handler: handler}
}

func (d *nullDebouncer) Submit(event EventCallback) {
	d.handler(event)
}

func (d *nullDebouncer) Stop() {}

// --- TimedDebouncer: waits for quiet period, fires latest only, seq-aware ---

type timedDebouncer struct {
	duration time.Duration
	handler  EventHandler
	lastSeq  atomic.Uint64
	mu       sync.Mutex
	timer    *time.Timer
	latest   EventCallback
}

// NewTimedDebouncer creates a debouncer that waits for `duration` of quiet
// (no new events) before firing the handler with the most recent event.
// Uses sequence numbers to discard stale events — if an older event arrives
// after a newer one, it is silently dropped.
func NewTimedDebouncer(duration time.Duration, handler EventHandler) Debouncer {
	return &timedDebouncer{
		duration: duration,
		handler:  handler,
	}
}

func (d *timedDebouncer) Submit(event EventCallback) {
	// Seq-based stale detection: drop events older than what we've seen.
	if event.Seq > 0 {
		for {
			current := d.lastSeq.Load()
			if event.Seq <= current {
				return // stale, discard
			}
			if d.lastSeq.CompareAndSwap(current, event.Seq) {
				break
			}
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.latest = event

	// Reset timer.
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.duration, func() {
		d.mu.Lock()
		ev := d.latest
		d.mu.Unlock()
		d.handler(ev)
	})
}

func (d *timedDebouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
