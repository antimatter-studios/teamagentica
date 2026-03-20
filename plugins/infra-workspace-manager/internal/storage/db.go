package storage

import (
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// WorkspaceRecord tracks workspace-manager-level metadata for each workspace.
// The kernel only knows about containers; this table adds workspace semantics.
type WorkspaceRecord struct {
	ContainerID   string    `json:"container_id" gorm:"primaryKey"`
	EnvironmentID string    `json:"environment_id" gorm:"not null;index"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DB wraps the GORM connection for workspace storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the workspace manager database.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "workspaces.db", &WorkspaceRecord{})
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
