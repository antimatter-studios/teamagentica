package claudecli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// mcpServerEntry represents a single MCP server in Claude's config format.
type mcpServerEntry struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// mcpConfigFile is the structure of Claude's MCP config JSON.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// WriteMCPConfig writes or updates the Claude MCP config JSON file.
func WriteMCPConfig(claudeDir, serverURL string) error {
	configPath := filepath.Join(claudeDir, "mcp-config.json")

	cfg := mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{},
	}

	// Read existing config if present.
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Printf("[claude-cli] corrupt mcp-config.json, overwriting: %v", err)
			cfg.MCPServers = map[string]mcpServerEntry{}
		}
	}

	cfg.MCPServers["teamagentica"] = mcpServerEntry{
		Type: "sse",
		URL:  serverURL,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp config: %w", err)
	}

	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("create claude dir: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write mcp-config.json: %w", err)
	}

	log.Printf("[claude-cli] wrote MCP config to %s (url=%s)", configPath, serverURL)
	return nil
}

// RemoveMCPConfig removes the teamagentica entry from the MCP config.
func RemoveMCPConfig(claudeDir string) error {
	configPath := filepath.Join(claudeDir, "mcp-config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read mcp-config.json: %w", err)
	}

	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil // corrupt file, nothing to remove
	}

	delete(cfg.MCPServers, "teamagentica")

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp config: %w", err)
	}

	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return fmt.Errorf("write mcp-config.json: %w", err)
	}

	log.Printf("[claude-cli] removed MCP config from %s", configPath)
	return nil
}

// MCPConfigPath returns the path to the MCP config file.
func MCPConfigPath(claudeDir string) string {
	path := filepath.Join(claudeDir, "mcp-config.json")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}
