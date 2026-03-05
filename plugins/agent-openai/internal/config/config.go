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
	Backend        string // "subscription" or "api_key"
	CodexDataPath  string
	CodexCLIBinary string
	CodexCLITimeout int
	Debug          bool
}

// Load reads configuration from environment variables.
func Load() *Config {
	cfg := &Config{
		KernelHost:     envOrDefault("TEAMAGENTICA_KERNEL_HOST", "localhost"),
		KernelPort:     envOrDefault("TEAMAGENTICA_KERNEL_PORT", "8080"),
		PluginID:       envOrDefault("TEAMAGENTICA_PLUGIN_ID", "agent-openai"),
		PluginToken:    os.Getenv("TEAMAGENTICA_PLUGIN_TOKEN"),
		OpenAIAPIKey:   os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:    envOrDefault("OPENAI_MODEL", "gpt-4o"),
		OpenAIEndpoint: envOrDefault("OPENAI_API_ENDPOINT", "https://api.openai.com/v1"),
		Backend:        envOrDefault("OPENAI_BACKEND", "subscription"),
		CodexDataPath:   envOrDefault("CODEX_DATA_PATH", "/data"),
		CodexCLIBinary:  envOrDefault("CODEX_CLI_BINARY", "/usr/local/bin/codex"),
		CodexCLITimeout: intEnvOrDefault("CODEX_CLI_TIMEOUT", 300),
		Debug:           os.Getenv("PLUGIN_DEBUG") == "true",
	}

	portStr := envOrDefault("AGENT_OPENAI_PORT", "8081")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 8081
	}
	cfg.Port = port

	// When using Codex subscription and model is still the default, switch to
	// the default Codex-compatible model.
	if cfg.Backend == "subscription" && cfg.OpenAIModel == "gpt-4o" {
		cfg.OpenAIModel = "gpt-5.3-codex"
	}

	return cfg
}

func intEnvOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
