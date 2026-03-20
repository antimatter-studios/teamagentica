package config

import (
	"encoding/json"
	"fmt"
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
// Returns an error if the file exists but is corrupt — callers must not
// overwrite a corrupt config as that would destroy user data.
func Load() (*Config, error) {
	p := ConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("corrupt config %s: %w — fix or delete the file manually", p, err)
	}
	return &cfg, nil
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
// When updating, only non-zero fields from p are applied — existing
// values (especially Kernel config) are preserved.
func (c *Config) SetProfile(p Profile) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == p.Name {
			if p.URL != "" {
				c.Profiles[i].URL = p.URL
			}
			if p.Token != "" {
				c.Profiles[i].Token = p.Token
			}
			if p.Kernel.Image != "" {
				c.Profiles[i].Kernel.Image = p.Kernel.Image
			}
			if p.Kernel.Port != 0 {
				c.Profiles[i].Kernel.Port = p.Kernel.Port
			}
			if p.Kernel.Domain != "" {
				c.Profiles[i].Kernel.Domain = p.Kernel.Domain
			}
			if p.Kernel.DataDir != "" {
				c.Profiles[i].Kernel.DataDir = p.Kernel.DataDir
			}
			if p.Kernel.Name != "" {
				c.Profiles[i].Kernel.Name = p.Kernel.Name
			}
			if p.Kernel.NetworkName != "" {
				c.Profiles[i].Kernel.NetworkName = p.Kernel.NetworkName
			}
			if p.Kernel.Labels != nil {
				c.Profiles[i].Kernel.Labels = p.Kernel.Labels
			}
			// bools are always applied when any other Kernel field is set
			if p.Kernel.Image != "" || p.Kernel.Port != 0 || p.Kernel.Domain != "" {
				c.Profiles[i].Kernel.DevMode = p.Kernel.DevMode
			}
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
}
