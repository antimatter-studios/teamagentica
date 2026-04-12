package models

import (
	"encoding/json"
	"time"
)

type Plugin struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"not null"`
	Version      string    `json:"version" gorm:"not null"`
	Image        string    `json:"image"`
	ContainerID  string    `json:"-"`
	Status       string    `json:"status" gorm:"not null;default:'stopped'"`
	Host         string    `json:"host"`
	GRPCPort     int       `json:"grpc_port"`
	HTTPPort     int       `json:"http_port"`
	EventPort    int       `json:"event_port,omitempty"` // deprecated: events now go to HTTPPort via /events route
	Capabilities JSONStringList `json:"capabilities" gorm:"type:json"`
	Dependencies JSONStringList `json:"dependencies,omitempty" gorm:"type:json"` // required capability strings
	Marketplace  string    `json:"marketplace" gorm:"default:'local'"`
	Enabled      bool      `json:"enabled" gorm:"default:false"`
	System       bool      `json:"system" gorm:"default:false"` // system plugins are auto-installed and cannot be uninstalled
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Candidate fields — a dev or new-build container running alongside the primary.
	CandidateContainerID string    `json:"-"`
	CandidateImage       string    `json:"candidate_image,omitempty"`
	CandidateVersion     string    `json:"candidate_version,omitempty"`
	CandidateHost        string    `json:"candidate_host,omitempty"`
	CandidatePort        int       `json:"candidate_port,omitempty"`
	CandidateEventPort   int       `json:"-"`
	CandidateHealthy     bool      `json:"candidate_healthy,omitempty"`
	CandidateDeployedAt  time.Time `json:"candidate_deployed_at,omitempty"`
	CandidateLastSeen    time.Time `json:"-"`

	// Previous fields — stored on promote so rollback can restore.
	PreviousImage   string `json:"-"`
	PreviousVersion string `json:"-"`

	// WorkspaceSchema defines how to launch a workspace environment.
	// Only present on plugins with the workspace:environment capability.
	// Stored as JSON; see WorkspaceSchemaData for the typed structure.
	WorkspaceSchema JSONRawString `json:"workspace_schema,omitempty" gorm:"type:json"`

	// SharedDisks is a JSON array of shared disk mounts requested by the plugin.
	// Each entry has a disk name and target mount path. The kernel auto-creates
	// the disk directory and bind-mounts it into the plugin container.
	SharedDisks JSONRawString `json:"shared_disks,omitempty" gorm:"type:json"`

	// RequestedScopes is a JSON array of authorization scopes the plugin needs.
	RequestedScopes JSONStringList `json:"requested_scopes,omitempty" gorm:"type:json"`

	// ConfigSchema is a JSON string mapping env var names to ConfigSchemaField objects.
	// Example:
	//   {
	//     "OPENAI_API_KEY": {"type":"string","label":"API Key","required":true,"secret":true},
	//     "OPENAI_MODEL":   {"type":"select","label":"Model","default":"gpt-4o","options":["gpt-4o","gpt-4o-mini"]}
	//   }
	// The kernel uses this schema to (a) present config UI and (b) inject default values
	// as env vars when starting the plugin container.
	ConfigSchema JSONRawString `json:"config_schema" gorm:"type:json"`
}

// IsMetadataOnly returns true when the plugin has no runtime image and should
// not be started as a container. Such plugins exist purely as discoverable
// metadata (e.g. workspace environment definitions).
func (p *Plugin) IsMetadataOnly() bool {
	return p.Image == ""
}

// HasCandidate returns true when a candidate container is deployed.
func (p *Plugin) HasCandidate() bool {
	return p.CandidateHost != ""
}

// ClearCandidate resets all candidate fields.
func (p *Plugin) ClearCandidate() {
	p.CandidateContainerID = ""
	p.CandidateImage = ""
	p.CandidateVersion = ""
	p.CandidateHost = ""
	p.CandidatePort = 0
	p.CandidateEventPort = 0
	p.CandidateHealthy = false
	p.CandidateDeployedAt = time.Time{}
	p.CandidateLastSeen = time.Time{}
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// ConfigSchemaField describes a single configuration field for a plugin.
type ConfigSchemaField struct {
	Type        string       `json:"type"`                    // "string", "select", "number", "boolean", "text"
	Label       string       `json:"label"`                   // Human-readable label
	Required    bool         `json:"required,omitempty"`      // Whether the field must be set
	Secret      bool         `json:"secret,omitempty"`        // Whether to mask the value in UI
	ReadOnly    bool         `json:"readonly,omitempty"`      // Display-only; cannot be changed by the user
	Virtual     bool         `json:"virtual,omitempty"`       // Pass-through only; value is never persisted
	Default     string       `json:"default,omitempty"`       // Default value
	Options     []string     `json:"options,omitempty"`       // For "select" type
	Dynamic     bool         `json:"dynamic,omitempty"`       // Fetch options at runtime from plugin
	HelpText    string       `json:"help_text,omitempty"`     // Tooltip/description
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`  // Show only when another field matches a value
	OAuthMethod string       `json:"oauth_method,omitempty"`  // OAuth flow variant: "device_code" or "redirect_code"
	Order       int          `json:"order,omitempty"`         // Display order (lower = first)
}

// GetConfigSchema parses the ConfigSchema into a map of field name to ConfigSchemaField.
func (p *Plugin) GetConfigSchema() (map[string]ConfigSchemaField, error) {
	if len(p.ConfigSchema) == 0 {
		return nil, nil
	}
	var schema map[string]ConfigSchemaField
	if err := json.Unmarshal([]byte(p.ConfigSchema), &schema); err != nil {
		return nil, err
	}
	return schema, nil
}

// SetConfigSchema serializes a config schema map to JSON and stores it.
func (p *Plugin) SetConfigSchema(schema map[string]ConfigSchemaField) error {
	data, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	p.ConfigSchema = JSONRawString(data)
	return nil
}

// WorkspaceSchemaData describes how to launch a workspace environment.
type WorkspaceSchemaData struct {
	DisplayName  string            `json:"display_name" yaml:"display_name"`
	Description  string            `json:"description" yaml:"description"`
	Image        string            `json:"image" yaml:"image"`
	Port         int               `json:"port" yaml:"port"`
	Cmd          []string          `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	EnvDefaults  map[string]string `json:"env_defaults,omitempty" yaml:"env_defaults,omitempty"`
	DockerUser   string            `json:"docker_user,omitempty" yaml:"docker_user,omitempty"`
}

// GetWorkspaceSchema parses the stored JSON into a typed struct.
func (p *Plugin) GetWorkspaceSchema() *WorkspaceSchemaData {
	if len(p.WorkspaceSchema) == 0 {
		return nil
	}
	var ws WorkspaceSchemaData
	if err := json.Unmarshal([]byte(p.WorkspaceSchema), &ws); err != nil {
		return nil
	}
	return &ws
}

// SetWorkspaceSchema serializes the workspace schema to JSON and stores it.
func (p *Plugin) SetWorkspaceSchema(ws *WorkspaceSchemaData) error {
	data, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	p.WorkspaceSchema = JSONRawString(data)
	return nil
}

// SharedDiskEntry describes a disk mount for a plugin.
type SharedDiskEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`   // "shared" or "workspace"
	Target string `json:"target"`
}

// GetSharedDisks parses the stored JSON shared disks.
func (p *Plugin) GetSharedDisks() []SharedDiskEntry {
	if string(p.SharedDisks) == "" {
		return nil
	}
	var disks []SharedDiskEntry
	if err := json.Unmarshal([]byte(p.SharedDisks), &disks); err != nil {
		return nil
	}
	return disks
}

// SetSharedDisks serializes the shared disks to JSON for storage.
func (p *Plugin) SetSharedDisks(disks []SharedDiskEntry) {
	if len(disks) == 0 {
		p.SharedDisks = JSONRawString("")
		return
	}
	data, _ := json.Marshal(disks)
	p.SharedDisks = JSONRawString(data)
}

// GetCapabilities returns the capabilities as a string slice.
func (p *Plugin) GetCapabilities() []string {
	if p.Capabilities == nil {
		return []string{}
	}
	return []string(p.Capabilities)
}

// SetCapabilities stores a string slice as capabilities.
func (p *Plugin) SetCapabilities(caps []string) {
	p.Capabilities = JSONStringList(caps)
}

// GetRequestedScopes returns the requested authorization scopes as a string slice.
func (p *Plugin) GetRequestedScopes() []string {
	if p.RequestedScopes == nil {
		return nil
	}
	return []string(p.RequestedScopes)
}

// SetRequestedScopes stores requested authorization scopes.
func (p *Plugin) SetRequestedScopes(scopes []string) {
	p.RequestedScopes = JSONStringList(scopes)
}

// GetDependencies returns the required capabilities as a string slice.
func (p *Plugin) GetDependencies() []string {
	if p.Dependencies == nil {
		return nil
	}
	return []string(p.Dependencies)
}

// SetDependencies stores required capability strings.
func (p *Plugin) SetDependencies(deps []string) {
	p.Dependencies = JSONStringList(deps)
}
