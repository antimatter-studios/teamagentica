package handlers

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// newTestHandler creates a PluginHandler with an in-memory SQLite DB for testing.
// It auto-migrates the required models.
func newTestHandler(t *testing.T) (*PluginHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Plugin{},
		&models.Event{},
		&models.EventSubscription{},
		&models.Config{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	h := &PluginHandler{
		db:     db,
		Events: events.NewHub(),
		Subs:   events.NewPersistentSubscriptionManager(db),
	}
	return h, db
}

// --- handleAddressedEvent tests ---

func TestHandleAddressedEvent_PersistsToDB(t *testing.T) {
	h, db := newTestHandler(t)

	// Create target plugin (stopped — dispatch won't succeed, event stays queued).
	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "stopped",
	})

	h.handleAddressedEvent("source", "usage:report", `{"tokens":100}`, "target", time.Now())

	var count int64
	db.Model(&models.Event{}).Where("target_plugin_id = ?", "target").Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 pending event, got %d", count)
	}
}

func TestHandleAddressedEvent_UsesSubscriptionCallbackPath(t *testing.T) {
	h, db := newTestHandler(t)

	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "stopped",
	})

	// Register subscription with custom callback path.
	h.Subs.Subscribe("target", "usage:report", "/custom/callback")

	h.handleAddressedEvent("source", "usage:report", `{"tokens":100}`, "target", time.Now())

	var evt models.Event
	db.Where("target_plugin_id = ?", "target").First(&evt)
	if evt.CallbackPath != "/custom/callback" {
		t.Fatalf("expected callback /custom/callback, got %s", evt.CallbackPath)
	}
}

func TestHandleAddressedEvent_DefaultCallbackPath(t *testing.T) {
	h, db := newTestHandler(t)

	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "stopped",
	})
	// No subscription registered.

	h.handleAddressedEvent("source", "usage:report", `{"tokens":100}`, "target", time.Now())

	var evt models.Event
	db.Where("target_plugin_id = ?", "target").First(&evt)
	if evt.CallbackPath != "/events/usage" {
		t.Fatalf("expected default callback /events/usage, got %s", evt.CallbackPath)
	}
}

func TestHandleAddressedEvent_AttemptsDispatchToRunningPlugin(t *testing.T) {
	h, db := newTestHandler(t)

	// Plugin is "running" but host is unreachable — tryDispatch will fail,
	// event should stay in DB with attempts incremented.
	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "running", Host: "127.0.0.1", HTTPPort: 59999,
	})

	h.handleAddressedEvent("source", "usage:report", `{"tokens":100}`, "target", time.Now())

	var evt models.Event
	db.Where("target_plugin_id = ?", "target").First(&evt)
	if evt.ID == 0 {
		t.Fatal("expected event to remain in DB after failed dispatch")
	}
	if evt.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", evt.Attempts)
	}
}

// --- enforcePendingCap tests ---

func TestEnforcePendingCap_UnderLimit(t *testing.T) {
	h, db := newTestHandler(t)

	for i := 0; i < 5; i++ {
		db.Create(&models.Event{
			EventType:      "test",
			SourcePluginID: "src",
			TargetPluginID: "target",
			CallbackPath:   "/cb",
			Payload:        "{}",
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	h.enforcePendingCap("target", 10)

	var count int64
	db.Model(&models.Event{}).Where("target_plugin_id = ?", "target").Count(&count)
	if count != 5 {
		t.Fatalf("expected 5 events (under cap), got %d", count)
	}
}

func TestEnforcePendingCap_OverLimit(t *testing.T) {
	h, db := newTestHandler(t)

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 15; i++ {
		db.Create(&models.Event{
			EventType:      "test",
			SourcePluginID: "src",
			TargetPluginID: "target",
			CallbackPath:   "/cb",
			Payload:        "{}",
			CreatedAt:      baseTime.Add(time.Duration(i) * time.Second),
		})
	}

	h.enforcePendingCap("target", 10)

	var count int64
	db.Model(&models.Event{}).Where("target_plugin_id = ?", "target").Count(&count)
	if count != 10 {
		t.Fatalf("expected 10 events after cap enforcement, got %d", count)
	}

	// Verify the oldest were evicted: the earliest remaining should have created_at = baseTime+5s.
	var oldest models.Event
	db.Where("target_plugin_id = ?", "target").Order("created_at ASC").First(&oldest)
	expected := baseTime.Add(5 * time.Second)
	if !oldest.CreatedAt.Equal(expected) {
		t.Fatalf("expected oldest event at %v, got %v", expected, oldest.CreatedAt)
	}
}

func TestEnforcePendingCap_ExactlyAtLimit(t *testing.T) {
	h, db := newTestHandler(t)

	for i := 0; i < 10; i++ {
		db.Create(&models.Event{
			EventType:      "test",
			SourcePluginID: "src",
			TargetPluginID: "target",
			CallbackPath:   "/cb",
			Payload:        "{}",
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	h.enforcePendingCap("target", 10)

	var count int64
	db.Model(&models.Event{}).Where("target_plugin_id = ?", "target").Count(&count)
	if count != 10 {
		t.Fatalf("expected 10 events at exact cap, got %d", count)
	}
}

// --- flushPendingEvents tests ---

func TestFlushPendingEvents_NoEvents(t *testing.T) {
	h, db := newTestHandler(t)

	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "running", Host: "127.0.0.1", HTTPPort: 8080,
	})

	// Should not panic or error with no pending events.
	h.flushPendingEvents("target")

	var count int64
	db.Model(&models.Event{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 events, got %d", count)
	}
}

func TestFlushPendingEvents_PluginNotRunning(t *testing.T) {
	h, db := newTestHandler(t)

	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "stopped",
	})
	db.Create(&models.Event{
		EventType:      "test",
		SourcePluginID: "src",
		TargetPluginID: "target",
		CallbackPath:   "/cb",
		Payload:        "{}",
	})

	h.flushPendingEvents("target")

	// Event should still be there since plugin is stopped.
	var count int64
	db.Model(&models.Event{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected event to remain (plugin stopped), got %d", count)
	}
}

func TestFlushPendingEvents_FailedDispatchIncrementsAttempts(t *testing.T) {
	h, db := newTestHandler(t)

	// Plugin is running but host is unreachable.
	db.Create(&models.Plugin{
		ID: "target", Name: "Target", Version: "1.0", Image: "img",
		Status: "running", Host: "127.0.0.1", HTTPPort: 59999,
	})
	db.Create(&models.Event{
		EventType:      "test",
		SourcePluginID: "src",
		TargetPluginID: "target",
		CallbackPath:   "/cb",
		Payload:        "{}",
		Attempts:       0,
	})

	h.flushPendingEvents("target")

	var evt models.Event
	db.First(&evt)
	if evt.Attempts != 1 {
		t.Fatalf("expected attempts=1 after failed flush, got %d", evt.Attempts)
	}
}

func TestFlushPendingEvents_PluginNotFound(t *testing.T) {
	h, db := newTestHandler(t)

	db.Create(&models.Event{
		EventType:      "test",
		SourcePluginID: "src",
		TargetPluginID: "nonexistent",
		CallbackPath:   "/cb",
		Payload:        "{}",
	})

	// Should not panic when target plugin doesn't exist.
	h.flushPendingEvents("nonexistent")

	// Event should still be there.
	var count int64
	db.Model(&models.Event{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected event to remain, got %d", count)
	}
}
