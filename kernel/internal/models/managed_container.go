package models

import (
	"encoding/json"
	"time"
)

// ManagedContainer represents a container launched by a plugin and tracked
// by the kernel. Unlike plugins, managed containers don't run the SDK —
// they are raw workload containers (code-server, terminals, etc.).
type ManagedContainer struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	PluginID    string    `json:"plugin_id" gorm:"not null;index"`
	Name        string    `json:"name" gorm:"not null"`
	Image       string    `json:"image" gorm:"not null"`
	ContainerID string    `json:"-"`
	Status      string    `json:"status" gorm:"not null;default:'stopped'"`
	Port        int       `json:"port" gorm:"not null"`
	Subdomain   string    `json:"subdomain" gorm:"uniqueIndex"`
	VolumeName  string    `json:"volume_name"`
	Env         string    `json:"-" gorm:"type:text"`
	Cmd         string    `json:"-" gorm:"type:text"` // JSON array of command args
	DockerUser  string    `json:"-" gorm:"column:docker_user"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GetEnv parses the stored JSON env string into a map.
func (mc *ManagedContainer) GetEnv() map[string]string {
	if mc.Env == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(mc.Env), &m); err != nil {
		return nil
	}
	return m
}

// GetCmd parses the stored JSON cmd string into a slice.
func (mc *ManagedContainer) GetCmd() []string {
	if mc.Cmd == "" {
		return nil
	}
	var cmd []string
	if err := json.Unmarshal([]byte(mc.Cmd), &cmd); err != nil {
		return nil
	}
	return cmd
}

// SetCmd serializes the cmd slice to JSON for storage.
func (mc *ManagedContainer) SetCmd(cmd []string) {
	if len(cmd) == 0 {
		mc.Cmd = ""
		return
	}
	data, _ := json.Marshal(cmd)
	mc.Cmd = string(data)
}

// SetEnv serializes the env map to JSON for storage.
func (mc *ManagedContainer) SetEnv(env map[string]string) {
	if env == nil {
		mc.Env = "{}"
		return
	}
	data, err := json.Marshal(env)
	if err != nil {
		mc.Env = "{}"
		return
	}
	mc.Env = string(data)
}
