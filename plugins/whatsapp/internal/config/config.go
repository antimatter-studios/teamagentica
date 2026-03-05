package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	PluginID string
	Port     int
	DataPath string
	Debug    bool

	// WhatsApp Business Cloud API.
	AccessToken   string // Meta access token
	PhoneNumberID string // WhatsApp phone number ID
	VerifyToken   string // Webhook verification token (you choose this)
	AppSecret     string // App secret for signature verification (optional)

	Aliases      string // comma-separated nickname=target pairs
	DefaultAgent string // plugin ID of the coordinator brain agent
}

func Load() *Config {
	port := 8091
	if v := os.Getenv("PLUGIN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}
	dataPath := os.Getenv("PLUGIN_DATA_PATH")
	if dataPath == "" {
		dataPath = "/data"
	}
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "whatsapp-bot"
	}
	return &Config{
		PluginID:      pluginID,
		Port:          port,
		DataPath:      dataPath,
		Debug:         os.Getenv("PLUGIN_DEBUG") == "true",
		AccessToken:   os.Getenv("WHATSAPP_ACCESS_TOKEN"),
		PhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		VerifyToken:   os.Getenv("WHATSAPP_VERIFY_TOKEN"),
		AppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		Aliases:       os.Getenv("ALIASES"),
		DefaultAgent:  os.Getenv("DEFAULT_AGENT"),
	}
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
