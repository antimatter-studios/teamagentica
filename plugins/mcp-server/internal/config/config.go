package config

import (
	"os"
	"strconv"
)

type Config struct {
	PluginID string
	Port     int
	Debug    bool
}

func Load() *Config {
	port := 8081
	if v := os.Getenv("MCP_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	return &Config{
		PluginID: envOrDefault("TEAMAGENTICA_PLUGIN_ID", "mcp-server"),
		Port:     port,
		Debug:    os.Getenv("PLUGIN_DEBUG") == "true",
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
