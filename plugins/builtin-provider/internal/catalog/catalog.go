package catalog

import (
	"log"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PricingEntry holds default pricing for a single model.
type PricingEntry struct {
	Provider    string  `json:"provider" yaml:"provider"`
	Model       string  `json:"model" yaml:"model"`
	InputPer1M  float64 `json:"input_per_1m" yaml:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m" yaml:"output_per_1m"`
	CachedPer1M float64 `json:"cached_per_1m" yaml:"cached_per_1m"`
	PerRequest  float64 `json:"per_request" yaml:"per_request"`
	Currency    string  `json:"currency" yaml:"currency"`
}

// PluginGroup identifies the category a plugin belongs to.
const (
	GroupAgents         = "agents"
	GroupMessaging      = "messaging"
	GroupTools          = "tools"
	GroupStorage        = "storage"
	GroupInfrastructure = "infrastructure"
	GroupNetwork        = "network"
)

// GroupMeta holds display metadata for a plugin group.
type GroupMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

// Groups defines all known plugin groups in display order.
var Groups = []GroupMeta{
	{ID: GroupAgents, Name: "AI Agents", Description: "Chat and reasoning providers powered by LLMs", Order: 1},
	{ID: GroupMessaging, Name: "Messaging", Description: "Communication channels and chat interfaces", Order: 2},
	{ID: GroupTools, Name: "Tools", Description: "Image, video, and other AI generation tools", Order: 3},
	{ID: GroupStorage, Name: "Storage", Description: "File and data storage backends", Order: 4},
	{ID: GroupNetwork, Name: "Network", Description: "Tunnels, webhooks, and external connectivity", Order: 5},
	{ID: GroupInfrastructure, Name: "Infrastructure", Description: "Platform internals and system services", Order: 6},
}

// Entry represents a plugin available in the catalog.
type Entry struct {
	PluginID       string           `json:"plugin_id" yaml:"id"`
	Name           string           `json:"name" yaml:"name"`
	Description    string           `json:"description" yaml:"description"`
	Group          string           `json:"group" yaml:"group"`
	Version        string           `json:"version" yaml:"version"`
	Image          string           `json:"image" yaml:"image"`
	Author         string           `json:"author" yaml:"author"`
	Tags           []string         `json:"tags" yaml:"tags"`
	ConfigSchema   map[string]Field `json:"config_schema,omitempty" yaml:"config_schema,omitempty"`
	DefaultPricing []PricingEntry   `json:"default_pricing,omitempty" yaml:"default_pricing,omitempty"`
}

// GroupedCatalog holds entries organized by group.
type GroupedCatalog struct {
	Group   GroupMeta `json:"group"`
	Plugins []Entry  `json:"plugins"`
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

// Field describes a config field in the schema.
type Field struct {
	Type        string       `json:"type" yaml:"type"`
	Label       string       `json:"label" yaml:"label"`
	Required    bool         `json:"required,omitempty" yaml:"required,omitempty"`
	Secret      bool         `json:"secret,omitempty" yaml:"secret,omitempty"`
	Default     string       `json:"default,omitempty" yaml:"default,omitempty"`
	Options     []string     `json:"options,omitempty" yaml:"options,omitempty"`
	Dynamic     bool         `json:"dynamic,omitempty" yaml:"dynamic,omitempty"`
	HelpText    string       `json:"help_text,omitempty" yaml:"help_text,omitempty"`
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty" yaml:"visible_when,omitempty"`
	Order       int          `json:"order,omitempty" yaml:"order,omitempty"`
}

// catalog holds the loaded plugin entries.
var catalog []Entry

// LoadFile reads a combined catalog YAML file (array of entries).
func LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var entries []Entry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return err
	}

	catalog = entries
	log.Printf("catalog: loaded %d plugin(s) from %s", len(catalog), path)
	return nil
}

// ByGroup returns the catalog organized by group in display order.
func ByGroup() []GroupedCatalog {
	grouped := make(map[string][]Entry)
	for _, e := range catalog {
		g := e.Group
		if g == "" {
			g = GroupInfrastructure
		}
		grouped[g] = append(grouped[g], e)
	}

	var result []GroupedCatalog
	for _, gm := range Groups {
		plugins, ok := grouped[gm.ID]
		if !ok || len(plugins) == 0 {
			continue
		}
		sort.Slice(plugins, func(i, j int) bool {
			return plugins[i].Name < plugins[j].Name
		})
		result = append(result, GroupedCatalog{Group: gm, Plugins: plugins})
	}

	return result
}

// Search filters the catalog by query string (matches against ID, name, description, tags).
func Search(q string) []Entry {
	if q == "" {
		return catalog
	}

	q = strings.ToLower(q)
	var results []Entry
	for _, e := range catalog {
		if matches(e, q) {
			results = append(results, e)
		}
	}
	return results
}

func matches(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.PluginID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), q) {
		return true
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}
