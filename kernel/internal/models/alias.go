package models

import "time"

// Alias maps a short name (e.g. "codex") to a target string (e.g. "agent-openai:gpt-4o").
// The kernel stores raw target strings; the SDK alias package parses them.
type Alias struct {
	Name      string    `json:"name" gorm:"primaryKey"`
	Target    string    `json:"target" gorm:"not null"`
	PluginID  string    `json:"plugin_id" gorm:"index;default:''"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
