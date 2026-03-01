package config

import (
	"log"
	"os"
)

type Config struct {
	Host          string
	Port          string
	JWTSecret     string
	DBPath        string
	DockerNetwork string
	Runtime       string
	DataDir       string
	MTLSEnabled   bool
	AdvertiseHost string // Address plugins should use to connect back to the kernel
}

func Load() *Config {
	jwtSecret := os.Getenv("ROBOSLOP_JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("ROBOSLOP_JWT_SECRET environment variable is required")
	}

	host := getEnv("ROBOSLOP_KERNEL_HOST", "0.0.0.0")

	return &Config{
		Host:          host,
		Port:          getEnv("ROBOSLOP_KERNEL_PORT", "8080"),
		JWTSecret:     jwtSecret,
		DBPath:        getEnv("ROBOSLOP_DB_PATH", "./roboslop.db"),
		DockerNetwork: getEnv("ROBOSLOP_DOCKER_NETWORK", "roboslop"),
		Runtime:       getEnv("ROBOSLOP_RUNTIME", "docker"),
		DataDir:       getEnv("ROBOSLOP_DATA_DIR", "./data"),
		MTLSEnabled:   getEnv("ROBOSLOP_MTLS_ENABLED", "true") == "true",
		AdvertiseHost: getEnv("ROBOSLOP_KERNEL_ADVERTISE_HOST", host),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
