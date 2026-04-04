package pluginsdk

import (
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
