package events

import (
	"testing"
	"time"
)

func TestHub_EmitSetsTimestamp(t *testing.T) {
	hub := NewHub()
	hub.Emit(DebugEvent{Type: "test", PluginID: "p1"})

	history := hub.History(0)
	if len(history) != 1 {
		t.Fatalf("expected 1 event in history, got %d", len(history))
	}
	evt, ok := history[0].Data.(DebugEvent)
	if !ok {
		t.Fatalf("expected DebugEvent in history, got %T", history[0].Data)
	}
	if evt.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestHub_EmitPreservesTimestamp(t *testing.T) {
	hub := NewHub()
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	hub.Emit(DebugEvent{Type: "test", PluginID: "p1", Timestamp: ts})

	history := hub.History(0)
	evt, ok := history[0].Data.(DebugEvent)
	if !ok {
		t.Fatalf("expected DebugEvent in history, got %T", history[0].Data)
	}
	if !evt.Timestamp.Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, evt.Timestamp)
	}
}

func TestHub_SubscribeReceivesEvents(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	hub.Emit(DebugEvent{Type: "test", PluginID: "p1", Detail: "hello"})

	select {
	case msg := <-ch:
		if msg.Channel != "audit" {
			t.Fatalf("expected channel 'audit', got %q", msg.Channel)
		}
		evt, ok := msg.Data.(DebugEvent)
		if !ok {
			t.Fatalf("expected DebugEvent in message, got %T", msg.Data)
		}
		if evt.Type != "test" || evt.Detail != "hello" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestHub_MultipleSubscribers(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()
	defer hub.Unsubscribe(ch1)
	defer hub.Unsubscribe(ch2)

	hub.Emit(DebugEvent{Type: "test"})

	for i, ch := range []chan SSEMessage{ch1, ch2} {
		select {
		case msg := <-ch:
			if msg.Channel != "audit" {
				t.Fatalf("subscriber %d: expected channel 'audit', got %q", i, msg.Channel)
			}
			evt, ok := msg.Data.(DebugEvent)
			if !ok {
				t.Fatalf("subscriber %d: expected DebugEvent in message, got %T", i, msg.Data)
			}
			if evt.Type != "test" {
				t.Fatalf("subscriber %d: unexpected type %s", i, evt.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	hub.Unsubscribe(ch)

	hub.Emit(DebugEvent{Type: "test"})

	// Channel should be closed and drained.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	default:
		// closed channel returns zero value immediately, but empty closed channel
		// might also hit default. Either way, no event was delivered — that's correct.
	}
}

func TestHub_HistoryLimit(t *testing.T) {
	hub := NewHub()
	for i := 0; i < 10; i++ {
		hub.Emit(DebugEvent{Type: "test", Detail: string(rune('0' + i))})
	}

	all := hub.History(0)
	if len(all) != 10 {
		t.Fatalf("expected 10 events, got %d", len(all))
	}

	limited := hub.History(3)
	if len(limited) != 3 {
		t.Fatalf("expected 3 events, got %d", len(limited))
	}
	// Should be the most recent 3.
	evt, ok := limited[0].Data.(DebugEvent)
	if !ok {
		t.Fatalf("expected DebugEvent in history, got %T", limited[0].Data)
	}
	if evt.Detail != string(rune('0'+7)) {
		t.Fatalf("expected detail '7', got %s", evt.Detail)
	}
}

func TestHub_HistoryRingBuffer(t *testing.T) {
	hub := NewHub()
	// Emit more than maxHistory (500) events.
	for i := 0; i < 510; i++ {
		hub.Emit(DebugEvent{Type: "test", Status: i})
	}

	all := hub.History(0)
	if len(all) != maxHistory {
		t.Fatalf("expected %d events, got %d", maxHistory, len(all))
	}
	// First event in history should be event #10 (0-indexed).
	evt, ok := all[0].Data.(DebugEvent)
	if !ok {
		t.Fatalf("expected DebugEvent in history, got %T", all[0].Data)
	}
	if evt.Status != 10 {
		t.Fatalf("expected oldest event status=10, got %d", evt.Status)
	}
}

func TestMarshalSSEMessage(t *testing.T) {
	msg := SSEMessage{Channel: "audit", Data: DebugEvent{Type: "test", PluginID: "p1", Detail: "hello"}}
	data, err := MarshalSSEMessage(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}
