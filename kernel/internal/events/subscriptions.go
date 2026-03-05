package events

import (
	"log"
	"sync"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// Subscription represents a plugin's interest in a named event type.
type Subscription struct {
	PluginID     string // subscriber plugin ID
	EventType    string // e.g. "tunnel:ready"
	CallbackPath string // path on the subscriber plugin's HTTP server, e.g. "/events/tunnel"
}

// SubscriptionManager is an in-memory registry of inter-plugin event subscriptions.
type SubscriptionManager struct {
	mu   sync.RWMutex
	subs []Subscription
	db   *gorm.DB // nil for non-persistent (test) instances
}

// NewSubscriptionManager creates a new in-memory-only SubscriptionManager (for tests).
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{}
}

// NewPersistentSubscriptionManager creates a SubscriptionManager backed by the database.
// Loads existing subscriptions from DB on creation.
func NewPersistentSubscriptionManager(db *gorm.DB) *SubscriptionManager {
	sm := &SubscriptionManager{db: db}

	var rows []models.EventSubscription
	if err := db.Find(&rows).Error; err != nil {
		log.Printf("events: failed to load subscriptions from db: %v", err)
		return sm
	}

	for _, r := range rows {
		sm.subs = append(sm.subs, Subscription{
			PluginID:     r.PluginID,
			EventType:    r.EventType,
			CallbackPath: r.CallbackPath,
		})
	}

	if len(rows) > 0 {
		log.Printf("events: loaded %d subscriptions from db", len(rows))
	}

	return sm
}

// Subscribe registers a plugin's interest in an event type.
// If the same (pluginID, eventType) already exists, the callback path is updated.
func (sm *SubscriptionManager) Subscribe(pluginID, eventType, callbackPath string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update existing subscription if present.
	for i, s := range sm.subs {
		if s.PluginID == pluginID && s.EventType == eventType {
			sm.subs[i].CallbackPath = callbackPath
			if sm.db != nil {
				sm.db.Model(&models.EventSubscription{}).
					Where("plugin_id = ? AND event_type = ?", pluginID, eventType).
					Update("callback_path", callbackPath)
			}
			return
		}
	}

	sm.subs = append(sm.subs, Subscription{
		PluginID:     pluginID,
		EventType:    eventType,
		CallbackPath: callbackPath,
	})

	if sm.db != nil {
		sm.db.Create(&models.EventSubscription{
			PluginID:     pluginID,
			EventType:    eventType,
			CallbackPath: callbackPath,
		})
	}
}

// Unsubscribe removes a plugin's subscription to a specific event type.
func (sm *SubscriptionManager) Unsubscribe(pluginID, eventType string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for i, s := range sm.subs {
		if s.PluginID == pluginID && s.EventType == eventType {
			sm.subs = append(sm.subs[:i], sm.subs[i+1:]...)
			break
		}
	}

	if sm.db != nil {
		sm.db.Where("plugin_id = ? AND event_type = ?", pluginID, eventType).
			Delete(&models.EventSubscription{})
	}
}

// UnsubscribeAll removes all subscriptions for a plugin (e.g. when it deregisters).
func (sm *SubscriptionManager) UnsubscribeAll(pluginID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	filtered := sm.subs[:0]
	for _, s := range sm.subs {
		if s.PluginID != pluginID {
			filtered = append(filtered, s)
		}
	}
	sm.subs = filtered

	if sm.db != nil {
		sm.db.Where("plugin_id = ?", pluginID).Delete(&models.EventSubscription{})
	}
}

// GetSubscribers returns all subscriptions for a given event type.
func (sm *SubscriptionManager) GetSubscribers(eventType string) []Subscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []Subscription
	for _, s := range sm.subs {
		if s.EventType == eventType {
			result = append(result, s)
		}
	}
	return result
}

// FindSubscription returns the subscription for a specific plugin+event pair, if any.
func (sm *SubscriptionManager) FindSubscription(pluginID, eventType string) (Subscription, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, s := range sm.subs {
		if s.PluginID == pluginID && s.EventType == eventType {
			return s, true
		}
	}
	return Subscription{}, false
}
