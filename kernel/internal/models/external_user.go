package models

import "time"

// ExternalUser maps an external platform user ID to a teamagentica user.
type ExternalUser struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TeamagenticaUserID uint      `gorm:"index;not null" json:"teamagentica_user_id"`
	ExternalID     string    `gorm:"uniqueIndex:idx_ext_source;not null" json:"external_id"`
	Source         string    `gorm:"uniqueIndex:idx_ext_source;not null" json:"source"`
	Label          string    `json:"label"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
