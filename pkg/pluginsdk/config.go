package pluginsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Config holds the kernel connection info populated from environment variables.
type Config struct {
	KernelHost string // TEAMAGENTICA_KERNEL_HOST
	KernelPort string // TEAMAGENTICA_KERNEL_PORT
	TLSCert    string // TEAMAGENTICA_TLS_CERT
	TLSKey     string // TEAMAGENTICA_TLS_KEY
	TLSCA      string // TEAMAGENTICA_TLS_CA
	Candidate  bool   // TEAMAGENTICA_CANDIDATE — true if running as a candidate container
}

// LoadConfig reads plugin SDK config from environment variables.
func LoadConfig() Config {
	return Config{
		KernelHost: os.Getenv("TEAMAGENTICA_KERNEL_HOST"),
		KernelPort: os.Getenv("TEAMAGENTICA_KERNEL_PORT"),
		TLSCert:    os.Getenv("TEAMAGENTICA_TLS_CERT"),
		TLSKey:     os.Getenv("TEAMAGENTICA_TLS_KEY"),
		TLSCA:      os.Getenv("TEAMAGENTICA_TLS_CA"),
		Candidate:  os.Getenv("TEAMAGENTICA_CANDIDATE") == "true",
	}
}

/// FetchConfig retrieves the plugin's own configuration from the kernel API.
// Returns a map of config key → value (unmasked, including secrets).
func (c *Client) FetchConfig() (map[string]string, error) {
	url := fmt.Sprintf("%s/api/plugins/%s/self-config", c.kernelURL(), c.registration.ID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Config map[string]string `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Config, nil
}

// CopyPluginConfig fetches unmasked config from sourcePluginID and writes it to targetPluginID.
// Used to clone config when creating sidecar plugins from a base agent image.
func (c *Client) CopyPluginConfig(sourcePluginID, targetPluginID string) error {
	// Fetch source config via self-config endpoint (returns unmasked values).
	getURL := fmt.Sprintf("%s/api/plugins/%s/self-config", c.kernelURL(), sourcePluginID)
	req, err := http.NewRequest(http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("build get request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch config returned %d", resp.StatusCode)
	}

	var result struct {
		Config map[string]string `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if len(result.Config) == 0 {
		return nil
	}

	// Build PUT payload for target.
	payload := map[string]interface{}{}
	for k, v := range result.Config {
		payload[k] = map[string]string{"value": v}
	}
	body, _ := json.Marshal(map[string]interface{}{"config": payload})

	putURL := fmt.Sprintf("%s/api/plugins/%s/config", c.kernelURL(), targetPluginID)
	putReq, err := http.NewRequest(http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build put request: %w", err)
	}
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := c.httpClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("write config returned %d: %s", putResp.StatusCode, string(respBody))
	}
	return nil
}

// SetPluginConfig writes config values to a target plugin's config store.
// Values are merged on top of existing config (does not delete unspecified keys).
func (c *Client) SetPluginConfig(pluginID string, values map[string]string) error {
	payload := map[string]interface{}{}
	for k, v := range values {
		payload[k] = map[string]string{"value": v}
	}
	body, _ := json.Marshal(map[string]interface{}{"config": payload})

	putURL := fmt.Sprintf("%s/api/plugins/%s/config", c.kernelURL(), pluginID)
	req, err := http.NewRequest(http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("set config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set config returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
