package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	PluginID     string
	HTTPPort     int
	KernelHost   string
	KernelPort   string
	ServiceToken string
}

func Load() (*Config, error) {
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
		pluginID = "webhook-ingress"
	}

	httpPort := 9000
	if p := os.Getenv("WEBHOOK_INGRESS_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			httpPort = v
		}
	}

	return &Config{
		PluginID:     pluginID,
		HTTPPort:     httpPort,
		KernelHost:   host,
		KernelPort:   port,
		ServiceToken: serviceToken,
	}, nil
}
