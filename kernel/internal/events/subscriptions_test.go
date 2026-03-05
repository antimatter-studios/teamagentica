package events

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.EventSubscription{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// --- In-memory SubscriptionManager tests ---

func TestSubscribe_InMemory(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/events/tunnel")

	subs := sm.GetSubscribers("tunnel:ready")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}
	if subs[0].PluginID != "pluginA" || subs[0].CallbackPath != "/events/tunnel" {
		t.Fatalf("unexpected sub: %+v", subs[0])
	}
}

func TestSubscribe_UpdateCallbackPath(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/v1")
	sm.Subscribe("pluginA", "tunnel:ready", "/v2")

	subs := sm.GetSubscribers("tunnel:ready")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber after update, got %d", len(subs))
	}
	if subs[0].CallbackPath != "/v2" {
		t.Fatalf("expected callback /v2, got %s", subs[0].CallbackPath)
	}
}

func TestUnsubscribe(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/events/tunnel")
	sm.Subscribe("pluginA", "build:done", "/events/build")
	sm.Unsubscribe("pluginA", "tunnel:ready")

	if subs := sm.GetSubscribers("tunnel:ready"); len(subs) != 0 {
		t.Fatalf("expected 0 subscribers for tunnel:ready, got %d", len(subs))
	}
	if subs := sm.GetSubscribers("build:done"); len(subs) != 1 {
		t.Fatalf("expected 1 subscriber for build:done, got %d", len(subs))
	}
}

func TestUnsubscribeAll(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/a")
	sm.Subscribe("pluginA", "build:done", "/b")
	sm.Subscribe("pluginB", "tunnel:ready", "/c")

	sm.UnsubscribeAll("pluginA")

	if subs := sm.GetSubscribers("tunnel:ready"); len(subs) != 1 || subs[0].PluginID != "pluginB" {
		t.Fatalf("expected only pluginB for tunnel:ready, got %+v", subs)
	}
	if subs := sm.GetSubscribers("build:done"); len(subs) != 0 {
		t.Fatalf("expected 0 subscribers for build:done, got %d", len(subs))
	}
}

func TestGetSubscribers_NoMatch(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/a")

	if subs := sm.GetSubscribers("nonexistent"); len(subs) != 0 {
		t.Fatalf("expected 0, got %d", len(subs))
	}
}

func TestGetSubscribers_MultiplePlugins(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/a")
	sm.Subscribe("pluginB", "tunnel:ready", "/b")
	sm.Subscribe("pluginC", "build:done", "/c")

	subs := sm.GetSubscribers("tunnel:ready")
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscribers, got %d", len(subs))
	}
}

func TestFindSubscription_Found(t *testing.T) {
	sm := NewSubscriptionManager()
	sm.Subscribe("pluginA", "tunnel:ready", "/events/tunnel")

	sub, found := sm.FindSubscription("pluginA", "tunnel:ready")
	if !found {
		t.Fatal("expected to find subscription")
	}
	if sub.CallbackPath != "/events/tunnel" {
		t.Fatalf("expected /events/tunnel, got %s", sub.CallbackPath)
	}
}

func TestFindSubscription_NotFound(t *testing.T) {
	sm := NewSubscriptionManager()

	_, found := sm.FindSubscription("pluginA", "tunnel:ready")
	if found {
		t.Fatal("expected not found")
	}
}

// --- Persistent SubscriptionManager tests ---

func TestPersistent_SubscribeCreatesDBRow(t *testing.T) {
	db := testDB(t)
	sm := NewPersistentSubscriptionManager(db)
	sm.Subscribe("pluginA", "tunnel:ready", "/events/tunnel")

	// Verify in-memory.
	subs := sm.GetSubscribers("tunnel:ready")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}

	// Verify DB row.
	var count int64
	db.Model(&models.EventSubscription{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 DB row, got %d", count)
	}
}

func TestPersistent_UpdateCallbackUpdatesDB(t *testing.T) {
	db := testDB(t)
	sm := NewPersistentSubscriptionManager(db)
	sm.Subscribe("pluginA", "tunnel:ready", "/v1")
	sm.Subscribe("pluginA", "tunnel:ready", "/v2")

	var row models.EventSubscription
	db.Where("plugin_id = ? AND event_type = ?", "pluginA", "tunnel:ready").First(&row)
	if row.CallbackPath != "/v2" {
		t.Fatalf("expected DB callback /v2, got %s", row.CallbackPath)
	}

	// Still only one row.
	var count int64
	db.Model(&models.EventSubscription{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 DB row after update, got %d", count)
	}
}

func TestPersistent_UnsubscribeDeletesDBRow(t *testing.T) {
	db := testDB(t)
	sm := NewPersistentSubscriptionManager(db)
	sm.Subscribe("pluginA", "tunnel:ready", "/a")
	sm.Subscribe("pluginA", "build:done", "/b")

	sm.Unsubscribe("pluginA", "tunnel:ready")

	var count int64
	db.Model(&models.EventSubscription{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 DB row after unsubscribe, got %d", count)
	}
}

func TestPersistent_UnsubscribeAllDeletesDBRows(t *testing.T) {
	db := testDB(t)
	sm := NewPersistentSubscriptionManager(db)
	sm.Subscribe("pluginA", "e1", "/a")
	sm.Subscribe("pluginA", "e2", "/b")
	sm.Subscribe("pluginB", "e1", "/c")

	sm.UnsubscribeAll("pluginA")

	var count int64
	db.Model(&models.EventSubscription{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 DB row after unsubscribe all, got %d", count)
	}
}

func TestPersistent_LoadsFromDB(t *testing.T) {
	db := testDB(t)

	// Seed rows directly in DB.
	db.Create(&models.EventSubscription{PluginID: "pluginA", EventType: "tunnel:ready", CallbackPath: "/events/tunnel"})
	db.Create(&models.EventSubscription{PluginID: "pluginB", EventType: "build:done", CallbackPath: "/events/build"})

	// Create manager — it should load from DB.
	sm := NewPersistentSubscriptionManager(db)

	subs := sm.GetSubscribers("tunnel:ready")
	if len(subs) != 1 || subs[0].PluginID != "pluginA" {
		t.Fatalf("expected pluginA for tunnel:ready, got %+v", subs)
	}

	subs = sm.GetSubscribers("build:done")
	if len(subs) != 1 || subs[0].PluginID != "pluginB" {
		t.Fatalf("expected pluginB for build:done, got %+v", subs)
	}
}
