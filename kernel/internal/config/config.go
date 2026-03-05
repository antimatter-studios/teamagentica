package config

import (
	"log"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppName       string // User-visible brand name (default "TeamAgentica", override via APP_NAME env)
	Host          string
	Port          string
	JWTSecret     string
	DBPath        string
	DockerNetwork string
	Runtime       string
	DataDir       string
	MTLSEnabled   bool
	AdvertiseHost string // Address plugins should use to connect back to the kernel
	ProviderURL   string // Default marketplace provider URL
	DevMode        bool          // Use :dev image tags instead of :latest
	ProjectRoot    string        // Host path to project root (for dev mode source mounts)
	BackupInterval time.Duration // How often to snapshot the SQLite database (default 5m)
}

func Load() *Config {
	jwtSecret := os.Getenv("TEAMAGENTICA_JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("TEAMAGENTICA_JWT_SECRET environment variable is required")
	}

	host := getEnv("TEAMAGENTICA_KERNEL_HOST", "0.0.0.0")

	return &Config{
		AppName:       getEnv("APP_NAME", "TeamAgentica"),
		Host:          host,
		Port:          getEnv("TEAMAGENTICA_KERNEL_PORT", "8080"),
		JWTSecret:     jwtSecret,
		DBPath:        getEnv("TEAMAGENTICA_DB_PATH", "./database.db"),
		DockerNetwork: getEnv("TEAMAGENTICA_DOCKER_NETWORK", "teamagentica"),
		Runtime:       getEnv("TEAMAGENTICA_RUNTIME", "docker"),
		DataDir:       getEnv("TEAMAGENTICA_DATA_DIR", "./data"),
		MTLSEnabled:   getEnv("TEAMAGENTICA_MTLS_ENABLED", "true") == "true",
		AdvertiseHost: getEnv("TEAMAGENTICA_KERNEL_ADVERTISE_HOST", host),
		ProviderURL:   getEnv("TEAMAGENTICA_PROVIDER_URL", ""),
		DevMode:        getEnv("TEAMAGENTICA_DEV_MODE", "false") == "true",
		ProjectRoot:    getEnv("TEAMAGENTICA_PROJECT_ROOT", ""),
		BackupInterval: parseDuration("TEAMAGENTICA_BACKUP_INTERVAL", 5*time.Minute),
	}
}

// ResolveImage returns the image name with the tag adjusted for dev mode.
// In dev mode, :latest is replaced with :dev.
func (c *Config) ResolveImage(image string) string {
	if !c.DevMode {
		return image
	}
	if strings.HasSuffix(image, ":latest") {
		return strings.TrimSuffix(image, ":latest") + ":dev"
	}
	return image
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
