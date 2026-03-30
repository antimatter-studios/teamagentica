package kimicli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// mcpConfigFile is the JSON config format kimi-cli expects via --mcp-config-file.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	URL string `json:"url"`
}

// WriteMCPConfig writes the MCP config JSON file for kimi-cli.
// Returns the path to the config file.
func WriteMCPConfig(kimiHome, serverURL string) (string, error) {
	configPath := filepath.Join(kimiHome, "mcp-servers.json")

	config := mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{
			"teamagentica": {URL: serverURL},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal MCP config: %w", err)
	}

	if err := os.MkdirAll(kimiHome, 0755); err != nil {
		return "", fmt.Errorf("create kimi home: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return "", fmt.Errorf("write MCP config: %w", err)
	}

	log.Printf("[kimi-cli] wrote MCP config to %s (url=%s)", configPath, serverURL)
	return configPath, nil
}

// RemoveMCPConfig removes the MCP config file.
func RemoveMCPConfig(kimiHome string) error {
	configPath := filepath.Join(kimiHome, "mcp-servers.json")
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove MCP config: %w", err)
	}
	return nil
}
