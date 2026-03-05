package config

import (
	"os"
	"strconv"
)

type Config struct {
	PluginID string
	Port     int
	APIKey   string
	Model    string
	DataPath string
	Debug    bool
}

func Load() *Config {
	cfg := &Config{
		PluginID: envOrDefault("TEAMAGENTICA_PLUGIN_ID", "agent-kimi"),
		APIKey:   os.Getenv("KIMI_API_KEY"),
		Model:    envOrDefault("KIMI_MODEL", "kimi-k2-turbo-preview"),
		DataPath: envOrDefault("KIMI_DATA_PATH", "/data"),
		Debug:    os.Getenv("PLUGIN_DEBUG") == "true",
	}

	portStr := envOrDefault("AGENT_KIMI_PORT", "8081")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 8081
	}
	cfg.Port = port

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
