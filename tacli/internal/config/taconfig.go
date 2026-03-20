package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TAConfig is the declarative configuration for a TeamAgentica instance.
// Loaded from a .taconfig file (YAML) via --config flag.
type TAConfig struct {
	Kernel      KernelConfig            `yaml:"kernel"`
	Admin       AdminConfig             `yaml:"admin"`
	Marketplace []string                `yaml:"marketplace"`
	Plugins     map[string]PluginConfig `yaml:"plugins"`
}

// KernelConfig holds kernel container settings.
type KernelConfig struct {
	Name    string            `yaml:"name"`
	Image   string            `yaml:"image"`
	Port    int               `yaml:"port"`
	Domain  string            `yaml:"domain"`
	DataDir string            `yaml:"data_dir"`
	DevMode bool              `yaml:"dev_mode"`
	Labels  map[string]string `yaml:"labels"`
}

// AdminConfig holds the initial admin user credentials.
type AdminConfig struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
}

// PluginConfig holds per-plugin install/enable/config settings.
type PluginConfig struct {
	Enable bool              `yaml:"enable"`
	Config map[string]string `yaml:"config"`
}

// LoadTAConfig reads a .taconfig YAML file (local path or URL),
// expands ${VAR} and ${VAR:-default} references from the environment,
// and returns the parsed config.
func LoadTAConfig(path string) (*TAConfig, error) {
	data, err := readSource(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := expandEnvVars(string(data))

	var cfg TAConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// readSource reads from a local file or HTTP(S) URL.
func readSource(path string) ([]byte, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(path)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: status %d", path, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(path)
}

// envVarPattern matches ${VAR} and ${VAR:-default}.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnvVars replaces ${VAR} with os.Getenv(VAR) and
// ${VAR:-default} with the default if VAR is unset/empty.
func expandEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := envVarPattern.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		name := parts[1]
		fallback := parts[2]

		if val := os.Getenv(name); val != "" {
			return val
		}
		if fallback != "" {
			return fallback
		}
		return ""
	})
}

// applyDefaults fills in zero values with sensible defaults.
func applyDefaults(cfg *TAConfig) {
	if cfg.Kernel.Image == "" {
		cfg.Kernel.Image = "teamagentica-kernel:latest"
	}
	if cfg.Kernel.Port == 0 {
		cfg.Kernel.Port = 9741
	}
	if cfg.Kernel.Domain == "" {
		cfg.Kernel.Domain = "localhost"
	}
	if cfg.Kernel.DataDir == "" {
		cfg.Kernel.DataDir = "./data"
	}
}
