package models

import "time"

// EventLog is a persistent, append-only record of inter-plugin event dispatches.
// Unlike the Event table (pending delivery queue), EventLog entries are never deleted
// automatically, giving full observability into plugin-to-plugin communication.
type EventLog struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	EventType      string    `gorm:"index;not null" json:"event_type"`
	SourcePluginID string    `gorm:"index;not null" json:"source_plugin_id"`
	TargetPluginID string    `gorm:"index;not null" json:"target_plugin_id"`
	Status         string    `gorm:"not null" json:"status"` // delivered, queued, failed, evicted
	Detail         string    `gorm:"type:text" json:"detail"`
	CreatedAt      time.Time `json:"created_at"`
}
