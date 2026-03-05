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

// Event represents an addressed event awaiting successful delivery.
// GORM table: "events"
type Event struct {
	ID             uint      `gorm:"primaryKey"`
	EventType      string    `gorm:"index;not null"`
	SourcePluginID string    `gorm:"not null"`
	TargetPluginID string    `gorm:"index;not null"`
	CallbackPath   string    `gorm:"not null"`
	Payload        string    `gorm:"type:text;not null"`
	Attempts       int       `gorm:"default:0"`
	CreatedAt      time.Time
}
