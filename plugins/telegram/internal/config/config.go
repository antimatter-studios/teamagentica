package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the Telegram plugin.
type Config struct {
	TelegramToken string
	KernelHost    string
	KernelPort    string
	ServiceToken  string
	PluginID      string
	AllowedUsers  string
	PollTimeout   int
	HTTPPort      int
	Debug         bool
	Aliases       string // legacy: comma-separated nickname=target pairs (aliases now managed via kernel)
	DefaultAgent  string // plugin ID of the coordinator brain agent
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	serviceToken := os.Getenv("TEAMAGENTICA_PLUGIN_TOKEN")
	if serviceToken == "" {
		return nil, fmt.Errorf("TEAMAGENTICA_PLUGIN_TOKEN is required")
	}

	host := os.Getenv("TEAMAGENTICA_KERNEL_HOST")
	if host == "" {
		host = "localhost"
	}

	port := os.Getenv("TEAMAGENTICA_KERNEL_PORT")
	if port == "" {
		port = "8080"
	}

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "telegram-bot"
	}

	allowedUsers := os.Getenv("TELEGRAM_ALLOWED_USERS")

	pollTimeout := 60
	if v := os.Getenv("TELEGRAM_POLL_TIMEOUT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			pollTimeout = parsed
		}
	}

	httpPort := 8443
	if v := os.Getenv("TELEGRAM_HTTP_PORT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			httpPort = parsed
		}
	}

	debug := os.Getenv("PLUGIN_DEBUG") == "true" || os.Getenv("PLUGIN_DEBUG") == "1"

	return &Config{
		TelegramToken: token,
		KernelHost:    host,
		KernelPort:    port,
		ServiceToken:  serviceToken,
		PluginID:      pluginID,
		AllowedUsers:  allowedUsers,
		PollTimeout:   pollTimeout,
		HTTPPort:      httpPort,
		Debug:         debug,
		Aliases:       os.Getenv("ALIASES"),
		DefaultAgent:  os.Getenv("DEFAULT_AGENT"),
	}, nil
}

// KernelBaseURL returns the full base URL of the kernel API.
func (c *Config) KernelBaseURL() string {
	return fmt.Sprintf("http://%s:%s", c.KernelHost, c.KernelPort)
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

// ParseAllowedUsers parses the comma-separated TELEGRAM_ALLOWED_USERS into a
// set of Telegram user IDs. Returns nil if the list is empty (all users allowed).
func (c *Config) ParseAllowedUsers() map[int64]bool {
	if c.AllowedUsers == "" {
		return nil
	}

	allowed := make(map[int64]bool)
	for _, s := range strings.Split(c.AllowedUsers, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		allowed[id] = true
	}

	if len(allowed) == 0 {
		return nil
	}
	return allowed
}
