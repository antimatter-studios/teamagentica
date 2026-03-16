package models

import "time"

type Config struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	OwnerID   string    `json:"owner_id" gorm:"column:owner_id;not null;index;uniqueIndex:idx_owner_key"`
	Key       string    `json:"key" gorm:"not null;uniqueIndex:idx_owner_key"`
	Value     string    `json:"value" gorm:"not null"`
	IsSecret  bool      `json:"is_secret" gorm:"default:false"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Config) TableName() string { return "configs" }
