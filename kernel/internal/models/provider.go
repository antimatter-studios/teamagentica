package models

import "time"

type Provider struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Name      string    `json:"name" gorm:"not null;uniqueIndex"`
	URL       string    `json:"url" gorm:"not null"`
	System    bool      `json:"system" gorm:"default:false"`
	Enabled   bool      `json:"enabled" gorm:"default:true"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
