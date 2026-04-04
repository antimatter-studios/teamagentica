package storage

import (
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// WorkspaceRecord tracks workspace-manager-level metadata for each workspace.
// The kernel only knows about containers; this table adds workspace semantics.
type WorkspaceRecord struct {
	ContainerID   string         `json:"container_id" gorm:"primaryKey"`
	EnvironmentID string         `json:"environment_id" gorm:"not null;index"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

// EnvironmentRecord stores a registered workspace environment.
// Populated via push-based registration events from workspace-env plugins.
type EnvironmentRecord struct {
	PluginID     string         `json:"plugin_id" gorm:"primaryKey"`
	DisplayName  string         `json:"display_name"`
	Description  string         `json:"description"`
	Image        string         `json:"image"`
	Port         int            `json:"port"`
	Icon         string         `json:"icon"`
	DockerUser   string         `json:"docker_user"`
	Cmd          string         `json:"cmd"`           // JSON array
	ExtraCmdArgs string         `json:"extra_cmd_args"` // JSON array
	SharedMounts string         `json:"shared_mounts"`  // JSON array
	EnvDefaults  string         `json:"env_defaults"`   // JSON object
	Status       string         `json:"status" gorm:"default:healthy"` // healthy, degraded
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// DB wraps the GORM connection for workspace storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the workspace manager database.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "workspaces.db", &WorkspaceRecord{}, &EnvironmentRecord{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

// Put creates or updates a workspace record.
func (d *DB) Put(rec *WorkspaceRecord) error {
	return d.db.Save(rec).Error
}

// GetByContainerID returns the workspace record for a container.
func (d *DB) GetByContainerID(containerID string) (*WorkspaceRecord, error) {
	var rec WorkspaceRecord
	if err := d.db.First(&rec, "container_id = ?", containerID).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// Delete removes a workspace record by container ID.
func (d *DB) Delete(containerID string) error {
	return d.db.Delete(&WorkspaceRecord{}, "container_id = ?", containerID).Error
}

// UpsertEnvironment creates or updates an environment registration.
func (d *DB) UpsertEnvironment(rec *EnvironmentRecord) error {
	return d.db.Save(rec).Error
}

// ListEnvironments returns all registered environments.
func (d *DB) ListEnvironments() ([]EnvironmentRecord, error) {
	var recs []EnvironmentRecord
	if err := d.db.Find(&recs).Error; err != nil {
		return nil, err
	}
	return recs, nil
}

// GetEnvironment returns a single environment by plugin ID.
func (d *DB) GetEnvironment(pluginID string) (*EnvironmentRecord, error) {
	var rec EnvironmentRecord
	if err := d.db.First(&rec, "plugin_id = ?", pluginID).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// SetEnvironmentStatus updates the status of a registered environment.
func (d *DB) SetEnvironmentStatus(pluginID, status string) error {
	return d.db.Model(&EnvironmentRecord{}).Where("plugin_id = ?", pluginID).Update("status", status).Error
}

// DeleteEnvironment removes an environment registration.
func (d *DB) DeleteEnvironment(pluginID string) error {
	return d.db.Delete(&EnvironmentRecord{}, "plugin_id = ?", pluginID).Error
}
