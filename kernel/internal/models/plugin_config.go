package models

import "time"

type PluginConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	PluginID  string    `json:"plugin_id" gorm:"not null;index;uniqueIndex:idx_plugin_key"`
	Key       string    `json:"key" gorm:"not null;uniqueIndex:idx_plugin_key"`
	Value     string    `json:"value" gorm:"not null"`
	IsSecret  bool      `json:"is_secret" gorm:"default:false"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
