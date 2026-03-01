package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration for the Discord plugin.
type Config struct {
	DiscordToken  string
	KernelHost    string
	KernelPort    string
	ServiceToken  string
	PluginID      string
	AgentConfigID *uint
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	token := os.Getenv("ROBOSLOP_DISCORD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("ROBOSLOP_DISCORD_TOKEN is required")
	}

	serviceToken := os.Getenv("ROBOSLOP_SERVICE_TOKEN")
	if serviceToken == "" {
		return nil, fmt.Errorf("ROBOSLOP_SERVICE_TOKEN is required")
	}

	host := os.Getenv("ROBOSLOP_KERNEL_HOST")
	if host == "" {
		host = "localhost"
	}

	port := os.Getenv("ROBOSLOP_KERNEL_PORT")
	if port == "" {
		port = "8080"
	}

	pluginID := os.Getenv("ROBOSLOP_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "discord-bot"
	}

	cfg := &Config{
		DiscordToken: token,
		KernelHost:   host,
		KernelPort:   port,
		ServiceToken: serviceToken,
		PluginID:     pluginID,
	}

	if idStr := os.Getenv("ROBOSLOP_AGENT_CONFIG_ID"); idStr != "" {
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid ROBOSLOP_AGENT_CONFIG_ID: %w", err)
		}
		uid := uint(id)
		cfg.AgentConfigID = &uid
	}

	return cfg, nil
}

// KernelBaseURL returns the full base URL of the kernel API.
func (c *Config) KernelBaseURL() string {
	return fmt.Sprintf("http://%s:%s", c.KernelHost, c.KernelPort)
}
