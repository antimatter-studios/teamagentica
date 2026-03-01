package models

import "time"

type ServiceToken struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"not null;uniqueIndex"`
	TokenHash    string    `json:"-" gorm:"not null"` // SHA256 hash of the token (for revocation lookup)
	Capabilities string    `json:"capabilities"`       // JSON array
	IssuedBy     uint      `json:"issued_by"`          // User ID of admin who created it
	ExpiresAt    time.Time `json:"expires_at"`
	Revoked      bool      `json:"revoked" gorm:"default:false"`
	CreatedAt    time.Time `json:"created_at"`
}
