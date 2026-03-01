package config

import (
	"os"
	"strconv"
)

// Config holds all configuration for the agent-openai plugin.
type Config struct {
	KernelHost     string
	KernelPort     string
	PluginID       string
	PluginToken    string
	Port           int
	OpenAIAPIKey   string
	OpenAIModel    string
	OpenAIEndpoint string
}

// Load reads configuration from environment variables.
func Load() *Config {
	cfg := &Config{
		KernelHost:     envOrDefault("ROBOSLOP_KERNEL_HOST", "localhost"),
		KernelPort:     envOrDefault("ROBOSLOP_KERNEL_PORT", "8080"),
		PluginID:       envOrDefault("ROBOSLOP_PLUGIN_ID", "agent-openai"),
		PluginToken:    os.Getenv("ROBOSLOP_PLUGIN_TOKEN"),
		OpenAIAPIKey:   os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:    envOrDefault("OPENAI_MODEL", "gpt-4o"),
		OpenAIEndpoint: envOrDefault("OPENAI_API_ENDPOINT", "https://api.openai.com/v1"),
	}

	portStr := envOrDefault("AGENT_OPENAI_PORT", "8081")
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
