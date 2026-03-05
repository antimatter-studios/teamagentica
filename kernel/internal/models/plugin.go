package models

import (
	"encoding/json"
	"time"
)

type Plugin struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Name         string    `json:"name" gorm:"not null"`
	Version      string    `json:"version" gorm:"not null"`
	Image        string    `json:"image" gorm:"not null"`
	ContainerID  string    `json:"-"`
	Status       string    `json:"status" gorm:"not null;default:'stopped'"`
	Host         string    `json:"host"`
	GRPCPort     int       `json:"grpc_port"`
	HTTPPort     int       `json:"http_port"`
	EventPort    int       `json:"event_port"` // ephemeral port for SDK event callbacks (0 = use HTTPPort)
	Capabilities JSONStringList `json:"capabilities" gorm:"type:json"`
	Marketplace  string    `json:"marketplace" gorm:"default:'local'"`
	Enabled      bool      `json:"enabled" gorm:"default:false"`
	System       bool      `json:"system" gorm:"default:false"` // system plugins are auto-installed and cannot be uninstalled
	ServiceToken string    `json:"-"` // internal token for plugin-to-kernel auth
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

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
	Default     string       `json:"default,omitempty"`       // Default value
	Options     []string     `json:"options,omitempty"`       // For "select" type
	Dynamic     bool         `json:"dynamic,omitempty"`       // Fetch options at runtime from plugin
	HelpText    string       `json:"help_text,omitempty"`     // Tooltip/description
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`  // Show only when another field matches a value
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
