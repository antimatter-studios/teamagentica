package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// StorageFile holds metadata returned by storage list operations.
type StorageFile struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	LastModified string `json:"last_modified"`
	ETag         string `json:"etag"`
}

// StorageListResult holds the response from StorageList.
type StorageListResult struct {
	Objects []StorageFile `json:"objects"`
	Count   int           `json:"count"`
}

// StorageBrowseResult holds the response from StorageBrowse.
type StorageBrowseResult struct {
	Prefix  string        `json:"prefix"`
	Folders []string      `json:"folders"`
	Files   []StorageFile `json:"files"`
}

// resolveStoragePlugin finds the plugin with tool:storage capability.
// Caches the result on the Client instance; retries on failure.
func (c *Client) resolveStoragePlugin() (string, error) {
	c.storageMu.RLock()
	if c.storagePluginID != "" {
		id := c.storagePluginID
		c.storageMu.RUnlock()
		return id, nil
	}
	c.storageMu.RUnlock()

	c.storageMu.Lock()
	defer c.storageMu.Unlock()

	// Double-check after acquiring write lock.
	if c.storagePluginID != "" {
		return c.storagePluginID, nil
	}

	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/search?capability=storage:api", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var result struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Plugins) == 0 {
		return "", fmt.Errorf("no storage plugin found")
	}

	c.storagePluginID = result.Plugins[0].ID
	log.Printf("pluginsdk: resolved storage plugin: %s", c.storagePluginID)
	return c.storagePluginID, nil
}

// storageRoute builds the kernel proxy URL for a storage operation.
func (c *Client) storageRoute(path string) (string, error) {
	pluginID, err := c.resolveStoragePlugin()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/api/route/%s%s", c.kernelURL(), pluginID, path), nil
}

// StorageWrite uploads data to the storage plugin.
func (c *Client) StorageWrite(ctx context.Context, key string, data io.Reader, contentType string) error {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return fmt.Errorf("storage write: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, data)
	if err != nil {
		return fmt.Errorf("storage write: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storage write: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage write: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// StorageRead downloads data from the storage plugin.
// Returns the body reader (caller must close), content type, and error.
func (c *Client) StorageRead(ctx context.Context, key string) (io.ReadCloser, string, error) {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: %w", err)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("storage read: status %d", resp.StatusCode)
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// StorageDelete removes an object from the storage plugin.
func (c *Client) StorageDelete(ctx context.Context, key string) error {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("storage delete: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage delete: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// StorageList returns all objects matching the given prefix.
func (c *Client) StorageList(ctx context.Context, prefix string) (*StorageListResult, error) {
	url, err := c.storageRoute("/list?prefix=" + prefix)
	if err != nil {
		return nil, fmt.Errorf("storage list: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("storage list: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("storage list: status %d", resp.StatusCode)
	}

	var result StorageListResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("storage list: decode: %w", err)
	}
	return &result, nil
}
