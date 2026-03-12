package models

import (
	"encoding/json"
	"time"
)

// ExtraMount describes an additional bind mount for a managed container.
type ExtraMount struct {
	// VolumeName is a path relative to the storage-volume volumes dir (same
	// convention as the primary VolumeName). In dev mode it resolves to a host
	// bind mount; in prod it maps to a named-volume subpath.
	VolumeName string `json:"volume_name"`
	Target     string `json:"target"` // mount path inside the container
	ReadOnly   bool   `json:"read_only,omitempty"`
}

// ManagedContainer represents a container launched by a plugin and tracked
// by the kernel. Unlike plugins, managed containers don't run the SDK —
// they are raw workload containers (code-server, terminals, etc.).
type ManagedContainer struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	PluginID   string    `json:"plugin_id" gorm:"not null;index"`
	Name       string    `json:"name" gorm:"not null"`
	Image         string    `json:"image" gorm:"not null"`
	ContainerID   string    `json:"-"`
	Status        string    `json:"status" gorm:"not null;default:'stopped'"`
	Port          int       `json:"port" gorm:"not null"`
	Subdomain     string    `json:"subdomain" gorm:"uniqueIndex"`
	VolumeName    string    `json:"volume_name"`
	ExtraMounts   string    `json:"-" gorm:"type:text"` // JSON array of ExtraMount
	Env           string    `json:"-" gorm:"type:text"`
	Cmd           string    `json:"-" gorm:"type:text"` // JSON array of command args
	DockerUser    string    `json:"-" gorm:"column:docker_user"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// GetExtraMounts parses the stored JSON extra mounts string.
func (mc *ManagedContainer) GetExtraMounts() []ExtraMount {
	if mc.ExtraMounts == "" {
		return nil
	}
	var mounts []ExtraMount
	if err := json.Unmarshal([]byte(mc.ExtraMounts), &mounts); err != nil {
		return nil
	}
	return mounts
}

// SetExtraMounts serializes the extra mounts slice to JSON for storage.
func (mc *ManagedContainer) SetExtraMounts(mounts []ExtraMount) {
	if len(mounts) == 0 {
		mc.ExtraMounts = ""
		return
	}
	data, _ := json.Marshal(mounts)
	mc.ExtraMounts = string(data)
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
