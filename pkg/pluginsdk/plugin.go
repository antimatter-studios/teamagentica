package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PluginInfo holds plugin metadata returned by SearchPlugins.
type PluginInfo struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	Image           string                 `json:"image"`
	Status          string                 `json:"status"`
	Capabilities    []string               `json:"capabilities"`
	WorkspaceSchema map[string]interface{} `json:"workspace_schema,omitempty"`
}

// SearchPlugins queries the kernel for plugins whose capabilities match the given prefix.
func (c *Client) SearchPlugins(capabilityPrefix string) ([]PluginInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/search?capability="+capabilityPrefix, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var result struct {
		Plugins []PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Plugins, nil
}

// GetPluginSchema fetches the live schema from a plugin via P2P (peer registry).
// Falls back to kernel proxy if the peer is unreachable.
// Returns the full schema map with sections like "config", "workspace", etc.
func (c *Client) GetPluginSchema(pluginID string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := c.RouteToPlugin(ctx, pluginID, "GET", "/schema", nil)
	if err != nil {
		return nil, fmt.Errorf("get schema from %s: %w", pluginID, err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	return schema, nil
}

// PluginStatus returns the current status of a plugin by querying the kernel.
// Returns the status string ("running", "stopped", "unhealthy", "error", "enabled")
// or empty string on failure. This is a lightweight call — the kernel only
// fetches the status fields, not the full plugin object.
func (c *Client) PluginStatus(pluginID string) string {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/"+pluginID+"/status", nil)
	if err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		return ""
	}
	return result.Status
}
