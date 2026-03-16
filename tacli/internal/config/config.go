package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// KernelState stores the configuration used to create/recreate the kernel container.
type KernelState struct {
	Image       string            `json:"image,omitempty"`
	Port        int               `json:"port,omitempty"`
	Domain      string            `json:"domain,omitempty"`
	DataDir     string            `json:"data_dir,omitempty"`
	Name        string            `json:"name,omitempty"`
	DevMode     bool              `json:"dev_mode"`
	MTLS        bool              `json:"mtls"`
	NetworkName string            `json:"network_name,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Profile stores connection details and kernel config for an instance.
type Profile struct {
	Name   string      `json:"name"`
	URL    string      `json:"url"`
	Token  string      `json:"token,omitempty"`
	Kernel KernelState `json:"kernel"`
}

// Config holds CLI configuration.
type Config struct {
	ActiveProfile string    `json:"active_profile"`
	Profiles      []Profile `json:"profiles"`
}

// ConfigPath returns ~/.tacli/config.json.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tacli", "config.json")
}

// Load reads the config file, returning defaults if it doesn't exist.
func Load() *Config {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return &Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}
	}
	return &cfg
}

// Save writes the config file to disk.
func Save(cfg *Config) error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Active returns the currently active profile, or nil.
func (c *Config) Active() *Profile {
	for i := range c.Profiles {
		if c.Profiles[i].Name == c.ActiveProfile {
			return &c.Profiles[i]
		}
	}
	return nil
}

// SetProfile adds or updates a profile.
func (c *Config) SetProfile(p Profile) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == p.Name {
			c.Profiles[i] = p
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
}
