package models

import "time"

// EventSubscription persists a plugin's interest in a named event type.
type EventSubscription struct {
	ID           uint   `gorm:"primaryKey"`
	PluginID     string `gorm:"uniqueIndex:idx_sub_plugin_event;not null"`
	EventType    string `gorm:"uniqueIndex:idx_sub_plugin_event;not null"`
	CallbackPath string `gorm:"not null"`
	CreatedAt    time.Time
}
