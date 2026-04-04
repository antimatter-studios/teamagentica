package pluginsdk

import (
	"encoding/json"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest represents the plugin.yaml file — the single source of truth
// for a plugin's identity, capabilities, dependencies, and config schema.
type Manifest struct {
	ID             string                       `yaml:"id"`
	Name           string                       `yaml:"name"`
	Description    string                       `yaml:"description"`
	Group          string                       `yaml:"group"`
	Version        string                       `yaml:"version"`
	Image          string                       `yaml:"image"`
	Author         string                       `yaml:"author"`
	Tags           []string                     `yaml:"tags"`
	Capabilities   []string                     `yaml:"capabilities"`
	Dependencies   []string                     `yaml:"dependencies"`
	ConfigSchema   map[string]ConfigSchemaField `yaml:"config_schema"`
	DefaultPricing []PricingEntry               `yaml:"default_pricing"`
}

// LoadManifest reads plugin.yaml from the current working directory (or the
// standard system config path) and returns the parsed manifest.
func LoadManifest() Manifest {
	candidates := []string{
		"plugin.yaml",                                    // dev mode (air, local run)
		"/usr/local/etc/teamagentica/plugin.yaml",        // production containers
	}

	var data []byte
	var err error
	for _, path := range candidates {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Fatalf("pluginsdk: failed to load plugin.yaml (tried %v): %v", candidates, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		log.Fatalf("pluginsdk: failed to parse plugin.yaml: %v", err)
	}
	if m.ID == "" {
		log.Fatalf("pluginsdk: plugin.yaml missing required 'id' field")
	}
	return m
}

// SelectOption represents a select field option with a display label and API value.
// It can be unmarshaled from either a plain string or a {label, value} object.
type SelectOption struct {
	Label string `json:"label" yaml:"label"`
	Value string `json:"value" yaml:"value"`
}

// UnmarshalYAML allows SelectOption to be parsed from a plain string or a map.
func (o *SelectOption) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string first
	var s string
	if err := unmarshal(&s); err == nil {
		o.Label = s
		o.Value = s
		return nil
	}
	// Try map
	type raw struct {
		Label string `yaml:"label"`
		Value string `yaml:"value"`
	}
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	o.Label = r.Label
	o.Value = r.Value
	return nil
}

// UnmarshalJSON allows SelectOption to be parsed from a plain string or a JSON object.
func (o *SelectOption) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		o.Label = s
		o.Value = s
		return nil
	}
	// Try object
	type raw struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	o.Label = r.Label
	o.Value = r.Value
	return nil
}

// ConfigSchemaField describes a single configuration field for a plugin.
type ConfigSchemaField struct {
	Type        string            `json:"type" yaml:"type"`
	Label       string            `json:"label" yaml:"label"`
	Required    bool              `json:"required,omitempty" yaml:"required,omitempty"`
	Secret      bool              `json:"secret,omitempty" yaml:"secret,omitempty"`
	ReadOnly    bool              `json:"readonly,omitempty" yaml:"readonly,omitempty"`
	Default     string            `json:"default,omitempty" yaml:"default,omitempty"`
	Options     []SelectOption    `json:"options,omitempty" yaml:"options,omitempty"`
	Dynamic     bool              `json:"dynamic,omitempty" yaml:"dynamic,omitempty"`
	HelpText    string            `json:"help_text,omitempty" yaml:"help_text,omitempty"`
	VisibleWhen *VisibleWhen      `json:"visible_when,omitempty" yaml:"visible_when,omitempty"`
	Order       int               `json:"order,omitempty" yaml:"order,omitempty"`
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

// SchemaFunc is called on each GET /schema request, allowing plugins to return
// a dynamic schema that reflects current config state. If nil, the static
// Schema/ConfigSchema/WorkspaceSchema fields are used instead.
type SchemaFunc func() map[string]interface{}
