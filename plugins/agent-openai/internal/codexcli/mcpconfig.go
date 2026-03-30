package codexcli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// WriteMCPConfig writes or updates the Codex config.toml with the MCP server entry.
func WriteMCPConfig(codexHome, serverURL string) error {
	configPath := filepath.Join(codexHome, "config.toml")

	// Read existing config if present.
	existing, _ := os.ReadFile(configPath)
	content := string(existing)

	mcpSection := fmt.Sprintf(`[mcp_servers.teamagentica]
url = "%s"
tool_timeout_sec = 120
enabled = true
`, serverURL)

	if strings.Contains(content, "[mcp_servers.teamagentica]") {
		content = replaceMCPSection(content, mcpSection)
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + mcpSection
	}

	// Ensure sandbox allows network access for MCP server connections.
	sandboxSection := `[sandbox_workspace_write]
network_access = true
`
	if !strings.Contains(content, "[sandbox_workspace_write]") {
		content += "\n" + sandboxSection
	}

	if err := os.MkdirAll(codexHome, 0755); err != nil {
		return fmt.Errorf("create codex home: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}
	// Ensure the file is readable by Codex CLI sandbox subprocess,
	// since WriteFile doesn't change permissions on existing files.
	os.Chmod(configPath, 0644)

	log.Printf("[codex-cli] wrote MCP config to %s (url=%s)", configPath, serverURL)
	return nil
}

// RemoveMCPConfig removes the [mcp_servers.teamagentica] section from config.toml.
func RemoveMCPConfig(codexHome string) error {
	configPath := filepath.Join(codexHome, "config.toml")

	existing, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Nothing to remove.
		}
		return fmt.Errorf("read config.toml: %w", err)
	}

	content := string(existing)
	if !strings.Contains(content, "[mcp_servers.teamagentica]") {
		return nil
	}

	content = replaceMCPSection(content, "")
	// Clean up excess blank lines.
	for strings.Contains(content, "\n\n\n") {
		content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	}
	content = strings.TrimRight(content, "\n") + "\n"

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}

	log.Printf("[codex-cli] removed MCP config from %s", configPath)
	return nil
}

// replaceMCPSection replaces the [mcp_servers.teamagentica] block in TOML content.
// The section ends at the next [header] or end of file.
func replaceMCPSection(content, replacement string) string {
	start := strings.Index(content, "[mcp_servers.teamagentica]")
	if start == -1 {
		return content
	}

	// Find the end of this section: next "[" at line start, or EOF.
	rest := content[start+len("[mcp_servers.teamagentica]"):]
	end := len(content)

	lines := strings.Split(rest, "\n")
	offset := start + len("[mcp_servers.teamagentica]")
	for _, line := range lines[1:] { // skip the header line itself
		offset += len(line) + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			// Found next section header. End before this line.
			end = offset - len(line) - 1
			break
		}
	}

	return content[:start] + replacement + content[end:]
}
