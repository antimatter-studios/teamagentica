package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration for the ngrok tunnel plugin.
type Config struct {
	PluginID       string
	KernelHost     string
	KernelPort     string
	ServiceToken   string
	NgrokAuthToken string
	NgrokDomain    string
	TunnelTarget   string
	HTTPPort       int
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	serviceToken := os.Getenv("TEAMAGENTICA_PLUGIN_TOKEN")
	if serviceToken == "" {
		return nil, fmt.Errorf("TEAMAGENTICA_PLUGIN_TOKEN is required")
	}

	ngrokAuthToken := os.Getenv("NGROK_AUTHTOKEN")
	if ngrokAuthToken == "" {
		return nil, fmt.Errorf("NGROK_AUTHTOKEN is required")
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
		pluginID = "ngrok"
	}

	ngrokDomain := os.Getenv("NGROK_DOMAIN")

	tunnelTarget := os.Getenv("NGROK_TUNNEL_TARGET")
	if tunnelTarget == "" {
		tunnelTarget = fmt.Sprintf("%s:%s", host, port)
	}

	httpPort := 9100
	if v := os.Getenv("NGROK_HTTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			httpPort = p
		}
	}

	return &Config{
		PluginID:       pluginID,
		KernelHost:     host,
		KernelPort:     port,
		ServiceToken:   serviceToken,
		NgrokAuthToken: ngrokAuthToken,
		NgrokDomain:    ngrokDomain,
		TunnelTarget:   tunnelTarget,
		HTTPPort:       httpPort,
	}, nil
}

// KernelBaseURL returns the full base URL of the kernel API.
func (c *Config) KernelBaseURL() string {
	return fmt.Sprintf("http://%s:%s", c.KernelHost, c.KernelPort)
}
