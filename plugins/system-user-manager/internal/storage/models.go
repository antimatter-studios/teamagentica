package storage

import "time"

// Setting is a simple key-value store for plugin-internal config (e.g. JWT secret).
type Setting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `gorm:"not null" json:"value"`
}

// User represents a platform user account.
type User struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	DisplayName  string    `json:"display_name"`
	Role         string    `gorm:"default:user" json:"role"`
	Banned       bool      `gorm:"default:false" json:"banned"`
	BanReason    string    `json:"ban_reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ServiceToken represents an API service token.
type ServiceToken struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"not null;uniqueIndex"`
	TokenHash    string    `json:"-" gorm:"not null"`
	Capabilities string    `json:"capabilities"`
	IssuedBy     uint      `json:"issued_by"`
	ExpiresAt    time.Time `json:"expires_at"`
	Revoked      bool      `json:"revoked" gorm:"default:false"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuditLog records security-relevant events.
type AuditLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Timestamp time.Time `json:"timestamp" gorm:"autoCreateTime;index"`
	ActorType string    `json:"actor_type" gorm:"not null"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action" gorm:"not null;index"`
	Resource  string    `json:"resource"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	Success   bool      `json:"success" gorm:"default:true"`
}

// ExternalUser maps an external platform user ID to a teamagentica user.
type ExternalUser struct {
	ID                 uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	TeamagenticaUserID uint      `gorm:"index;not null" json:"teamagentica_user_id"`
	ExternalID         string    `gorm:"uniqueIndex:idx_ext_source;not null" json:"external_id"`
	Source             string    `gorm:"uniqueIndex:idx_ext_source;not null" json:"source"`
	Label              string    `json:"label"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// RoleCapabilities maps roles to their granted capabilities.
var RoleCapabilities = map[string][]string{
	"admin": {
		"users:read",
		"users:write",
		"plugins:manage",
		"plugins:search",
		"system:admin",
	},
	"user": {
		"users:read:self",
		"plugins:search",
	},
}

// GetCapabilities returns the capability list for a given role.
func GetCapabilities(role string) []string {
	if caps, ok := RoleCapabilities[role]; ok {
		return caps
	}
	return []string{}
}
