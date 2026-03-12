package msgbuffer

import (
	"sync"
	"testing"
	"time"
)

func TestBuffer_SingleMessage(t *testing.T) {
	var mu sync.Mutex
	var flushed []struct {
		channelID string
		text      string
		mediaURLs []string
	}

	buf := New(50*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		flushed = append(flushed, struct {
			channelID string
			text      string
			mediaURLs []string
		}{channelID, text, mediaURLs})
		mu.Unlock()
	})

	buf.Add("c1", "hello", nil)
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("expected 1 flush, got %d", len(flushed))
	}
	if flushed[0].channelID != "c1" {
		t.Errorf("expected channel=c1, got %q", flushed[0].channelID)
	}
	if flushed[0].text != "hello" {
		t.Errorf("expected text=hello, got %q", flushed[0].text)
	}
}

func TestBuffer_MergesMultipleMessages(t *testing.T) {
	var mu sync.Mutex
	var result string

	buf := New(100*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		result = text
		mu.Unlock()
	})

	buf.Add("c1", "line 1", nil)
	time.Sleep(30 * time.Millisecond)
	buf.Add("c1", "line 2", nil)
	time.Sleep(30 * time.Millisecond)
	buf.Add("c1", "line 3", nil)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if result != "line 1\nline 2\nline 3" {
		t.Errorf("expected merged text, got %q", result)
	}
}

func TestBuffer_DeduplicatesMedia(t *testing.T) {
	var mu sync.Mutex
	var mediaResult []string

	buf := New(50*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		mediaResult = mediaURLs
		mu.Unlock()
	})

	buf.Add("c1", "msg1", []string{"http://a.png", "http://b.png"})
	buf.Add("c1", "msg2", []string{"http://b.png", "http://c.png"})
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(mediaResult) != 3 {
		t.Fatalf("expected 3 unique media URLs, got %d: %v", len(mediaResult), mediaResult)
	}
}

func TestBuffer_IndependentChannels(t *testing.T) {
	var mu sync.Mutex
	results := make(map[string]string)

	buf := New(50*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		results[channelID] = text
		mu.Unlock()
	})

	buf.Add("c1", "hello", nil)
	buf.Add("c2", "world", nil)
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if results["c1"] != "hello" {
		t.Errorf("c1: expected 'hello', got %q", results["c1"])
	}
	if results["c2"] != "world" {
		t.Errorf("c2: expected 'world', got %q", results["c2"])
	}
}

func TestBuffer_DebounceResets(t *testing.T) {
	var mu sync.Mutex
	flushCount := 0

	buf := New(80*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	// Send messages every 30ms — should keep resetting the 80ms timer.
	for i := 0; i < 5; i++ {
		buf.Add("c1", "msg", nil)
		time.Sleep(30 * time.Millisecond)
	}
	// Wait for the final debounce to fire.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if flushCount != 1 {
		t.Errorf("expected 1 flush (debounced), got %d", flushCount)
	}
}

func TestBuffer_SetDuration(t *testing.T) {
	var mu sync.Mutex
	flushed := false

	buf := New(500*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		flushed = true
		mu.Unlock()
	})

	// Shorten duration.
	buf.SetDuration(30 * time.Millisecond)
	buf.Add("c1", "fast", nil)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !flushed {
		t.Error("expected flush with shortened duration")
	}
}

func TestBuffer_Stop(t *testing.T) {
	var mu sync.Mutex
	results := make(map[string]string)

	buf := New(10*time.Second, func(channelID, text string, mediaURLs []string) {
		mu.Lock()
		results[channelID] = text
		mu.Unlock()
	})

	buf.Add("c1", "pending1", nil)
	buf.Add("c2", "pending2", nil)
	buf.Stop()

	mu.Lock()
	defer mu.Unlock()
	if results["c1"] != "pending1" {
		t.Errorf("c1 not flushed on stop: %q", results["c1"])
	}
	if results["c2"] != "pending2" {
		t.Errorf("c2 not flushed on stop: %q", results["c2"])
	}
}

func TestBuffer_EmptyNotFlushed(t *testing.T) {
	flushCount := 0
	buf := New(20*time.Millisecond, func(channelID, text string, mediaURLs []string) {
		flushCount++
	})

	buf.Add("c1", "", nil)
	time.Sleep(80 * time.Millisecond)

	if flushCount != 0 {
		t.Errorf("expected no flush for empty message, got %d", flushCount)
	}
}
