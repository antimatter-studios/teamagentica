package pluginsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ManagedContainerInfo represents a managed container tracked by the kernel.
type ManagedContainerInfo struct {
	ID       string `json:"id"`
	PluginID string `json:"plugin_id"`
	Name     string `json:"name"`
	Image         string `json:"image"`
	Status        string `json:"status"`
	Port          int    `json:"port"`
	Subdomain     string `json:"subdomain"`
	VolumeName    string `json:"volume_name"`
}

// ExtraMount describes an additional bind mount for a managed container.
type ExtraMount struct {
	VolumeName string `json:"volume_name"`        // volume dir name (same convention as primary VolumeName)
	Target     string `json:"target"`              // mount path inside the container
	ReadOnly   bool   `json:"read_only,omitempty"` // mount as read-only
}

// CreateManagedContainerRequest is the body for creating a managed container.
type CreateManagedContainerRequest struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Port          int               `json:"port"`
	Subdomain     string            `json:"subdomain"`
	VolumeName  string       `json:"volume_name,omitempty"`
	ExtraMounts []ExtraMount `json:"extra_mounts,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Cmd           []string          `json:"cmd,omitempty"`
	DockerUser    string            `json:"docker_user,omitempty"`
	PluginSource  string            `json:"plugin_source,omitempty"` // plugin name whose source to bind-mount for dev editing
}

// UpdateManagedContainerRequest is the body for patching a managed container.
type UpdateManagedContainerRequest struct {
	Name       *string `json:"name,omitempty"`
	Subdomain  *string `json:"subdomain,omitempty"`
	VolumeName *string `json:"volume_name,omitempty"`
}

// CreateManagedContainer asks the kernel to launch a managed container.
func (c *Client) CreateManagedContainer(req CreateManagedContainerRequest) (*ManagedContainerInfo, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// ListManagedContainers returns all managed containers owned by this plugin.
func (c *Client) ListManagedContainers() ([]ManagedContainerInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/containers", nil)
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

	var containers []ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return containers, nil
}

// StartManagedContainer re-launches a stopped managed container.
func (c *Client) StartManagedContainer(containerID string) (*ManagedContainerInfo, error) {
	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers/"+containerID+"/start", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// StopManagedContainer stops a managed container but keeps its record so it can be re-started.
func (c *Client) StopManagedContainer(containerID string) (*ManagedContainerInfo, error) {
	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers/"+containerID+"/stop", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// DeleteManagedContainer stops and removes a managed container.
func (c *Client) DeleteManagedContainer(containerID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.kernelURL()+"/api/plugins/containers/"+containerID, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// UpdateManagedContainer patches a managed container's metadata.
func (c *Client) UpdateManagedContainer(containerID string, req UpdateManagedContainerRequest) (*ManagedContainerInfo, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPatch, c.kernelURL()+"/api/plugins/containers/"+containerID, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}
