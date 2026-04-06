package pluginsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CreatePluginRequest is the body for programmatically creating a plugin in the kernel.
type CreatePluginRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Image        string   `json:"image"`
	HTTPPort     int      `json:"http_port"`
	Capabilities []string `json:"capabilities"`
}

// EnablePluginRequest optionally overrides shared_disks when enabling a plugin.
type EnablePluginRequest struct {
	SharedDisks []SharedDiskOverride `json:"shared_disks,omitempty"`
}

// SharedDiskOverride specifies a disk mount for a dynamically-created plugin.
type SharedDiskOverride struct {
	Name   string `json:"name"`
	Type   string `json:"type"`   // "shared" or "workspace"
	Target string `json:"target"` // mount path inside the container
}

// CreatePlugin registers a new plugin in the kernel's database.
// The plugin is not started until EnablePlugin is called.
func (c *Client) CreatePlugin(req CreatePluginRequest) error {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// EnablePlugin starts a plugin's container. Optionally overrides shared_disks
// to mount workspace-specific disks into the container.
func (c *Client) EnablePlugin(pluginID string, req *EnablePluginRequest) error {
	var body io.Reader
	if req != nil {
		b, _ := json.Marshal(req)
		body = bytes.NewReader(b)
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/"+pluginID+"/enable", body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// DisablePlugin stops a plugin's container.
func (c *Client) DisablePlugin(pluginID string) error {
	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/"+pluginID+"/disable", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// DeletePlugin removes a plugin from the kernel's database.
// The plugin must be disabled first.
func (c *Client) DeletePlugin(pluginID string) error {
	httpReq, err := http.NewRequest(http.MethodDelete, c.kernelURL()+"/api/plugins/"+pluginID, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// GetPlugin fetches a plugin's info from the kernel.
func (c *Client) GetPlugin(pluginID string) (map[string]interface{}, error) {
	httpReq, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/"+pluginID, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var plugin map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&plugin); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return plugin, nil
}
