package config

import (
	"os"
	"strconv"
)

type Config struct {
	PluginID string
	Port     int
	DataPath string
	Debug    bool
}

func Load() *Config {
	port := 8090
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
		pluginID = "cost-explorer"
	}
	return &Config{
		PluginID: pluginID,
		Port:     port,
		DataPath: dataPath,
		Debug:    os.Getenv("PLUGIN_DEBUG") == "true",
	}
}
