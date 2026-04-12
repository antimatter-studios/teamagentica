package kimicli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// WriteConfig writes the kimi-cli config.toml with provider credentials.
// A default model entry is created so the CLI knows which provider to use.
// Per-request model override via --model flag still works.
func WriteConfig(kimiHome, apiKey, defaultModel string) error {
	configPath := filepath.Join(kimiHome, "config.toml")

	if defaultModel == "" {
		defaultModel = "kimi-k2.5"
	}

	config := fmt.Sprintf(`default_model = "%s"
default_yolo = true

[providers.kimi]
type = "kimi"
base_url = "https://api.moonshot.ai/v1"
api_key = "%s"

[models."%s"]
provider = "kimi"
model = "%s"
max_context_size = 131072

[loop_control]
max_steps_per_turn = 50
max_retries_per_step = 3

[mcp.client]
tool_call_timeout_ms = 120000
`, defaultModel, apiKey, defaultModel, defaultModel)

	if err := os.MkdirAll(kimiHome, 0755); err != nil {
		return fmt.Errorf("create kimi home: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}

	log.Printf("[kimi-cli] wrote config.toml to %s", configPath)
	return nil
}
