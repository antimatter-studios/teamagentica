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
	Subdomain     string         `json:"subdomain"`
	DiskName      string         `json:"disk_name"`
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
	Disks        string         `json:"disks"`          // JSON array of WorkspaceDiskSpec
	EnvDefaults  string         `json:"env_defaults"`   // JSON object
	Status       string         `json:"status" gorm:"default:healthy"` // healthy, degraded
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// WorkspaceDisk describes a disk mounted into a workspace container.
type WorkspaceDisk struct {
	DiskID   string `json:"disk_id"`              // stable storage-disk ID
	Name     string `json:"name"`                 // display name
	Type     string `json:"type"`                 // "workspace" or "shared"
	Target   string `json:"target"`               // mount path inside the container
	ReadOnly bool   `json:"read_only,omitempty"`
}

// WorkspaceOptions stores per-workspace overrides on top of environment defaults.
type WorkspaceOptions struct {
	ContainerID  string    `json:"container_id" gorm:"primaryKey"`
	EnvOverrides string    `json:"env_overrides" gorm:"type:text"` // JSON: {"KEY": "value"}
	Disks        string    `json:"disks" gorm:"type:text"`         // JSON: []WorkspaceDisk (all disks)
	AgentPlugin  string    `json:"agent_plugin"`                    // e.g. "agent-anthropic"
	AgentModel   string    `json:"agent_model"`                     // e.g. "claude-opus-4-6"
	SidecarID    string    `json:"sidecar_id"`                      // plugin ID once created
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DB wraps the GORM connection for workspace storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the workspace manager database.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "workspaces.db", &WorkspaceRecord{}, &EnvironmentRecord{}, &WorkspaceOptions{})
	if err != nil {
		return nil, err
	}
	d := &DB{db: conn}
	d.migrateAgentRenames()
	return d, nil
}

// migrateAgentRenames updates stale agent-claude references to agent-anthropic.
func (d *DB) migrateAgentRenames() {
	renames := map[string]string{
		"agent-claude": "agent-anthropic",
		"agent-gemini": "agent-google",
		"agent-kimi":   "agent-moonshot",
	}
	for oldName, newName := range renames {
		// Update agent_plugin field.
		d.db.Model(&WorkspaceOptions{}).Where("agent_plugin = ?", oldName).Update("agent_plugin", newName)

		// Update sidecar_id: ws-{subdomain}-agent-claude → ws-{subdomain}-agent-anthropic
		// Use REPLACE since sidecar_id contains the plugin name as a suffix.
		d.db.Exec("UPDATE workspace_options SET sidecar_id = REPLACE(sidecar_id, ?, ?) WHERE sidecar_id LIKE ?",
			oldName, newName, "%"+oldName+"%")

		// Update disk references in the disks JSON field.
		d.db.Exec("UPDATE workspace_options SET disks = REPLACE(disks, ?, ?) WHERE disks LIKE ?",
			oldName, newName, "%"+oldName+"%")
	}
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

// ListAllRecords returns all active (non-deleted) workspace records.
func (d *DB) ListAllRecords() ([]WorkspaceRecord, error) {
	var records []WorkspaceRecord
	if err := d.db.Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// UpdateContainerID atomically changes a workspace record's primary key.
func (d *DB) UpdateContainerID(oldID, newID string) error {
	return d.db.Exec("UPDATE workspace_records SET container_id = ? WHERE container_id = ? AND deleted_at IS NULL", newID, oldID).Error
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

// GetOptions returns the workspace options for a container, or nil if none set.
func (d *DB) GetOptions(containerID string) (*WorkspaceOptions, error) {
	var opts WorkspaceOptions
	if err := d.db.First(&opts, "container_id = ?", containerID).Error; err != nil {
		return nil, err
	}
	return &opts, nil
}

// PutOptions creates or updates workspace options.
func (d *DB) PutOptions(opts *WorkspaceOptions) error {
	return d.db.Save(opts).Error
}

// ListAllOptions returns all workspace options records.
func (d *DB) ListAllOptions() ([]WorkspaceOptions, error) {
	var opts []WorkspaceOptions
	if err := d.db.Find(&opts).Error; err != nil {
		return nil, err
	}
	return opts, nil
}

// DeleteOptions removes workspace options by container ID.
func (d *DB) DeleteOptions(containerID string) error {
	return d.db.Delete(&WorkspaceOptions{}, "container_id = ?", containerID).Error
}
