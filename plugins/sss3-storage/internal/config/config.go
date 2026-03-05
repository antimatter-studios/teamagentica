package config

import (
	"os"
	"strconv"
)

type Config struct {
	PluginID    string
	Port        int
	S3Port      int // Local sss3 sidecar port
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	StoragePath string
	Debug       bool
}

func Load() *Config {
	cfg := &Config{
		PluginID:    envOrDefault("TEAMAGENTICA_PLUGIN_ID", "sss3-storage"),
		S3Bucket:    envOrDefault("S3_BUCKET", "teamagentica"),
		S3AccessKey: envOrDefault("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey: envOrDefault("S3_SECRET_KEY", "minioadmin"),
		S3Region:    envOrDefault("S3_REGION", "us-east-1"),
		StoragePath: envOrDefault("SSS3_STORAGE_PATH", "/data/sss3"),
		Debug:       os.Getenv("PLUGIN_DEBUG") == "true",
	}

	portStr := envOrDefault("SSS3_STORAGE_PORT", "8081")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 8081
	}
	cfg.Port = port

	s3PortStr := envOrDefault("SSS3_PORT", "5553")
	s3Port, err := strconv.Atoi(s3PortStr)
	if err != nil {
		s3Port = 5553
	}
	cfg.S3Port = s3Port

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
