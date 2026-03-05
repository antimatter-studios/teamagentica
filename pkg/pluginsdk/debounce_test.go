package pluginsdk

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNullDebouncer(t *testing.T) {
	var count atomic.Int32
	var lastDetail string

	d := NewNullDebouncer(func(e EventCallback) {
		count.Add(1)
		lastDetail = e.Detail
	})
	defer d.Stop()

	d.Submit(EventCallback{Detail: "a"})
	d.Submit(EventCallback{Detail: "b"})
	d.Submit(EventCallback{Detail: "c"})

	if count.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", count.Load())
	}
	if lastDetail != "c" {
		t.Fatalf("expected last detail 'c', got %q", lastDetail)
	}
}

func TestTimedDebouncerCoalesces(t *testing.T) {
	var count atomic.Int32
	var lastDetail atomic.Pointer[string]

	d := NewTimedDebouncer(100*time.Millisecond, func(e EventCallback) {
		count.Add(1)
		lastDetail.Store(&e.Detail)
	})
	defer d.Stop()

	// Fire 5 events in rapid succession.
	for i := 0; i < 5; i++ {
		detail := string(rune('a' + i))
		d.Submit(EventCallback{Detail: detail, Seq: uint64(i + 1)})
	}

	// Should not have fired yet.
	if count.Load() != 0 {
		t.Fatalf("expected 0 calls during debounce, got %d", count.Load())
	}

	// Wait for debounce to fire.
	time.Sleep(200 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatalf("expected 1 call after debounce, got %d", count.Load())
	}
	if p := lastDetail.Load(); p == nil || *p != "e" {
		t.Fatalf("expected last detail 'e', got %v", p)
	}
}

func TestTimedDebouncerSeqDiscardsStale(t *testing.T) {
	var lastDetail atomic.Pointer[string]

	d := NewTimedDebouncer(100*time.Millisecond, func(e EventCallback) {
		lastDetail.Store(&e.Detail)
	})
	defer d.Stop()

	// Send seq 5 first, then stale seq 3.
	d.Submit(EventCallback{Detail: "new", Seq: 5})
	d.Submit(EventCallback{Detail: "stale", Seq: 3})

	time.Sleep(200 * time.Millisecond)

	if p := lastDetail.Load(); p == nil || *p != "new" {
		t.Fatalf("expected 'new' (stale event should be discarded), got %v", p)
	}
}

func TestTimedDebouncerResets(t *testing.T) {
	var count atomic.Int32

	d := NewTimedDebouncer(100*time.Millisecond, func(e EventCallback) {
		count.Add(1)
	})
	defer d.Stop()

	// First event.
	d.Submit(EventCallback{Detail: "a", Seq: 1})

	// 80ms later — within debounce window — send another.
	time.Sleep(80 * time.Millisecond)
	d.Submit(EventCallback{Detail: "b", Seq: 2})

	// At t=80ms the timer was reset, so it shouldn't have fired yet at t=130ms.
	time.Sleep(50 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("expected 0 calls at t=130ms, got %d", count.Load())
	}

	// Wait for the reset timer to fire.
	time.Sleep(100 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatalf("expected 1 call after reset timer, got %d", count.Load())
	}
}

func TestTimedDebouncerNoSeq(t *testing.T) {
	// When seq is 0 (not set), all events should be accepted.
	var count atomic.Int32

	d := NewTimedDebouncer(50*time.Millisecond, func(e EventCallback) {
		count.Add(1)
	})
	defer d.Stop()

	d.Submit(EventCallback{Detail: "a"})
	d.Submit(EventCallback{Detail: "b"})

	time.Sleep(100 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatalf("expected 1 coalesced call, got %d", count.Load())
	}
}
