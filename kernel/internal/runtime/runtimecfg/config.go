package runtimecfg

import (
	"embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed runtime.prod.yaml
var configFS embed.FS

// Config defines how plugin and managed containers are set up.
// Loaded from runtime.dev.yaml or runtime.prod.yaml based on TEAMAGENTICA_DEV_MODE.
type Config struct {
	PluginEnv           map[string]string   `yaml:"plugin_env"`
	DataMount           DataMountConfig     `yaml:"data_mount"`
	StorageCrossMount   MountSpec           `yaml:"storage_cross_mount"`
	PluginMounts        []MountSpec         `yaml:"plugin_mounts"`
	ManagedDiskMount    MountSpec           `yaml:"managed_disk_mount"`
	ManagedExtraMount   MountSpec           `yaml:"managed_extra_mount"`
	ManagedPluginSource ManagedSourceConfig `yaml:"managed_plugin_source"`
}

// DataMountConfig controls the /data mount for plugin containers.
type DataMountConfig struct {
	Type   string `yaml:"type"` // "bind" or "volume"
	Source string `yaml:"source"`
}

// MountSpec describes a single mount (bind or volume).
type MountSpec struct {
	Type     string `yaml:"type"` // "bind" or "volume"
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	Subpath  string `yaml:"subpath"`
	ReadOnly bool   `yaml:"readonly"`
}

// ManagedSourceConfig controls plugin source mounting in managed containers.
type ManagedSourceConfig struct {
	Enabled     bool        `yaml:"enabled"`
	Source      string      `yaml:"source"`
	ExtraMounts []MountSpec `yaml:"extra_mounts"`
}

// Load reads the runtime config.
func Load() (*Config, error) {
	data, err := configFS.ReadFile("runtime.prod.yaml")
	if err != nil {
		return nil, fmt.Errorf("read runtime.prod.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runtime.prod.yaml: %w", err)
	}
	return &cfg, nil
}

// Resolve substitutes ${VAR} placeholders in a template string.
func Resolve(tmpl string, vars map[string]string) string {
	result := tmpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "${"+k+"}", v)
	}
	return result
}
