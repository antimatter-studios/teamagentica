package config

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	AppName       string // User-visible brand name (default "TeamAgentica", override via APP_NAME env)
	Host          string
	Port          string // HTTP port for user traffic (browser, tacli)
	TLSPort       string // HTTPS port for plugin mTLS traffic
	DBPath        string
	DockerNetwork string
	Runtime       string
	DataDir       string
	AdvertiseHost string // Address plugins should use to connect back to the kernel
	BackupInterval time.Duration // How often to snapshot the SQLite database (default 5m)
	BaseDomain     string        // Base domain for subdomain routing (e.g. "teamagentica.localhost")
}

func Load() *Config {
	host := getEnv("TEAMAGENTICA_KERNEL_HOST", "0.0.0.0")

	dataDir := filepath.Clean(os.Getenv("TEAMAGENTICA_DATA_DIR"))
	if dataDir == "." {
		log.Fatal("TEAMAGENTICA_DATA_DIR is required — set it to the host path that is bind-mounted at /data in this container")
	}

	return &Config{
		AppName:        getEnv("APP_NAME", "TeamAgentica"),
		Host:           host,
		Port:           getEnv("TEAMAGENTICA_KERNEL_PORT", "8080"),
		TLSPort:        getEnv("TEAMAGENTICA_KERNEL_TLS_PORT", "8081"),
		DBPath:         getEnv("TEAMAGENTICA_DB_PATH", "/data/kernel/database.db"),
		DockerNetwork:  getEnv("TEAMAGENTICA_DOCKER_NETWORK", "teamagentica"),
		Runtime:        getEnv("TEAMAGENTICA_RUNTIME", "docker"),
		DataDir:        dataDir,
		AdvertiseHost:  getEnv("TEAMAGENTICA_KERNEL_ADVERTISE_HOST", host),
		BackupInterval: parseDuration("TEAMAGENTICA_BACKUP_INTERVAL", 5*time.Minute),
		BaseDomain:     getEnv("TEAMAGENTICA_BASE_DOMAIN", "teamagentica.localhost"),
	}
}

func parseDuration(envKey string, fallback time.Duration) time.Duration {
	v := os.Getenv(envKey)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("config: invalid duration %s=%q, using default %s", envKey, v, fallback)
		return fallback
	}
	return d
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
