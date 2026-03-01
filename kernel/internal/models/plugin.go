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
	Capabilities string    `json:"capabilities"`
	Marketplace  string    `json:"marketplace" gorm:"default:'local'"`
	Enabled      bool      `json:"enabled" gorm:"default:false"`
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
	ConfigSchema string `json:"config_schema"`
}

// ConfigSchemaField describes a single configuration field for a plugin.
type ConfigSchemaField struct {
	Type     string   `json:"type"`               // "string", "select", "number", "boolean", "text"
	Label    string   `json:"label"`              // Human-readable label
	Required bool     `json:"required,omitempty"` // Whether the field must be set
	Secret   bool     `json:"secret,omitempty"`   // Whether to mask the value in UI
	Default  string   `json:"default,omitempty"`  // Default value
	Options  []string `json:"options,omitempty"`  // For "select" type
	HelpText string   `json:"help_text,omitempty"` // Tooltip/description
}

// GetConfigSchema parses the ConfigSchema JSON string into a map of field name to ConfigSchemaField.
func (p *Plugin) GetConfigSchema() (map[string]ConfigSchemaField, error) {
	if p.ConfigSchema == "" {
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
	p.ConfigSchema = string(data)
	return nil
}

// GetCapabilities parses the JSON capabilities string into a slice.
func (p *Plugin) GetCapabilities() []string {
	if p.Capabilities == "" {
		return []string{}
	}
	var caps []string
	if err := json.Unmarshal([]byte(p.Capabilities), &caps); err != nil {
		return []string{}
	}
	return caps
}

// SetCapabilities serializes a string slice to JSON and stores it.
func (p *Plugin) SetCapabilities(caps []string) {
	data, err := json.Marshal(caps)
	if err != nil {
		p.Capabilities = "[]"
		return
	}
	p.Capabilities = string(data)
}
