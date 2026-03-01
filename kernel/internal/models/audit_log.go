package models

import "time"

type AuditLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Timestamp time.Time `json:"timestamp" gorm:"autoCreateTime;index"`
	ActorType string    `json:"actor_type" gorm:"not null"` // "user", "service", "system"
	ActorID   string    `json:"actor_id"`                   // user ID, service token name, or "kernel"
	Action    string    `json:"action" gorm:"not null;index"` // "auth.login", "auth.register", "plugin.install", etc.
	Resource  string    `json:"resource"`                     // What was acted on: "plugin:discord-bot", "user:5", etc.
	Detail    string    `json:"detail"`                       // JSON blob with extra context
	IP        string    `json:"ip"`                           // Client IP
	Success   bool      `json:"success" gorm:"default:true"`
}
