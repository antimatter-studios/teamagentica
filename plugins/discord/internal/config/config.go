package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all configuration for the Discord plugin.
type Config struct {
	DiscordToken string
	PluginID     string
	Aliases      string // comma-separated nickname=target pairs
	DefaultAgent string // plugin ID of the coordinator brain agent
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	token := os.Getenv("TEAMAGENTICA_DISCORD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("TEAMAGENTICA_DISCORD_TOKEN is required")
	}

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "discord-bot"
	}

	return &Config{
		DiscordToken: token,
		PluginID:     pluginID,
		Aliases:      os.Getenv("ALIASES"),
		DefaultAgent: os.Getenv("DEFAULT_AGENT"),
	}, nil
}

// ParseAliases splits the comma-separated ALIASES config into individual entries.
func (c *Config) ParseAliases() []string {
	if c.Aliases == "" {
		return nil
	}
	var entries []string
	for _, s := range strings.Split(c.Aliases, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			entries = append(entries, s)
		}
	}
	return entries
}
