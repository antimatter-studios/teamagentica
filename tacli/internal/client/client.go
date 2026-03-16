package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a TeamAgentica kernel.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// New creates a client for the given kernel URL.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// HealthResponse is the kernel health check payload.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	App     string `json:"app"`
}

// Health calls GET /api/health.
func (c *Client) Health() (*HealthResponse, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/api/health", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("kernel is down (proxy returned %d) — is the kernel container running?", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("kernel not found at %s (got %d) — check the URL or verify the kernel container is running", c.BaseURL, resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected response (%d): %s", resp.StatusCode, string(b))
	}

	var h HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, fmt.Errorf("response is not a TA kernel (decode error: %w)", err)
	}

	if h.Status == "" || h.Version == "" {
		return nil, fmt.Errorf("endpoint responded but does not look like a TA kernel")
	}

	return &h, nil
}

// LoginRequest is POST /api/auth/login body.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is POST /api/auth/login response.
type LoginResponse struct {
	Token string          `json:"token"`
	User  json.RawMessage `json:"user"`
}

// Login authenticates and returns a JWT token.
func (c *Client) Login(email, password string) (*LoginResponse, error) {
	body, _ := json.Marshal(LoginRequest{Email: email, Password: password})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/auth/login", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed (%d): %s", resp.StatusCode, string(b))
	}

	var lr LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &lr, nil
}

// Register creates a new user account via POST /api/auth/register.
// The first registered user automatically becomes admin.
func (c *Client) Register(email, password string) (*LoginResponse, error) {
	body, _ := json.Marshal(LoginRequest{Email: email, Password: password})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/auth/register", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register failed (%d): %s", resp.StatusCode, string(b))
	}

	var lr LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &lr, nil
}

// PluginSummary is a plugin representation from the kernel API.
type PluginSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
	System  bool   `json:"system"`
}

// ListPlugins calls GET /api/plugins.
func (c *Client) ListPlugins() ([]PluginSummary, error) {
	resp, err := c.get("/api/plugins")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var wrapper struct {
		Plugins []PluginSummary `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return wrapper.Plugins, nil
}

// EnablePluginResponse is the response from POST /api/plugins/:id/enable.
type EnablePluginResponse struct {
	Message string   `json:"message"`
	Enabled []string `json:"enabled"`
}

// EnablePlugin calls POST /api/plugins/:id/enable.
func (c *Client) EnablePlugin(id string) (*EnablePluginResponse, error) {
	resp, err := c.post("/api/plugins/" + id + "/enable")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result EnablePluginResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &EnablePluginResponse{Enabled: []string{id}}, nil
	}
	return &result, nil
}

// DisablePlugin calls POST /api/plugins/:id/disable.
func (c *Client) DisablePlugin(id string) error {
	_, err := c.post("/api/plugins/" + id + "/disable")
	return err
}

// RestartPlugin calls POST /api/plugins/:id/restart.
func (c *Client) RestartPlugin(id string) error {
	_, err := c.post("/api/plugins/" + id + "/restart")
	return err
}

// UninstallPlugin calls DELETE /api/plugins/:id.
func (c *Client) UninstallPlugin(id string) error {
	return c.doSimple("DELETE", "/api/plugins/"+id)
}

// PluginConfig calls GET /api/plugins/:id/config and returns raw JSON.
func (c *Client) PluginConfig(id string) (json.RawMessage, error) {
	resp, err := c.get("/api/plugins/" + id + "/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return json.RawMessage(b), nil
}

// PluginSchema calls GET /api/plugins/:id/schema and returns raw JSON.
func (c *Client) PluginSchema(id string) (json.RawMessage, error) {
	resp, err := c.get("/api/plugins/" + id + "/schema")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return json.RawMessage(b), nil
}

// Provider is a marketplace provider from the kernel.
type Provider struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	System  bool   `json:"system"`
}

// ListProviders calls GET /api/marketplace/providers.
func (c *Client) ListProviders() ([]Provider, error) {
	resp, err := c.get("/api/marketplace/providers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var wrapper struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return wrapper.Providers, nil
}

// AddProvider calls POST /api/marketplace/providers.
func (c *Client) AddProvider(name, url string) (*Provider, error) {
	body, _ := json.Marshal(map[string]string{"name": name, "url": url})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/marketplace/providers", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(b))
	}

	var wrapper struct {
		Provider Provider `json:"provider"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &wrapper.Provider, nil
}

// DeleteProvider calls DELETE /api/marketplace/providers/:id.
func (c *Client) DeleteProvider(id string) error {
	return c.doSimple("DELETE", "/api/marketplace/providers/"+id)
}

// CatalogPlugin is a plugin entry from the marketplace catalog.
type CatalogPlugin struct {
	PluginID    string   `json:"plugin_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Version     string   `json:"version"`
	Author      string   `json:"author"`
	Provider    string   `json:"provider"`
	Tags        []string `json:"tags"`
}

// ProviderPlugins calls GET /api/marketplace/providers/:name/plugins.
func (c *Client) ProviderPlugins(name string) ([]CatalogPlugin, error) {
	resp, err := c.get("/api/marketplace/providers/" + name + "/plugins")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var wrapper struct {
		Plugins []CatalogPlugin `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return wrapper.Plugins, nil
}

// BrowseResult holds plugins and any provider fetch errors.
type BrowseResult struct {
	Plugins []CatalogPlugin
	Errors  []string
}

// BrowsePlugins calls GET /api/marketplace/plugins.
func (c *Client) BrowsePlugins() (*BrowseResult, error) {
	resp, err := c.get("/api/marketplace/plugins")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var wrapper struct {
		Plugins []CatalogPlugin `json:"plugins"`
		Errors  []string        `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &BrowseResult{Plugins: wrapper.Plugins, Errors: wrapper.Errors}, nil
}

// InstallPluginResponse is the response from POST /api/marketplace/install.
type InstallPluginResponse struct {
	Message   string          `json:"message"`
	Plugin    PluginSummary   `json:"plugin"`
	Installed []PluginSummary `json:"installed"`
}

// InstallPlugin calls POST /api/marketplace/install.
func (c *Client) InstallPlugin(pluginID string) (*InstallPluginResponse, error) {
	body, _ := json.Marshal(map[string]string{"plugin_id": pluginID})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/marketplace/install", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(b))
	}

	var result InstallPluginResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// UpgradePlugin calls POST /api/marketplace/upgrade.
func (c *Client) UpgradePlugin(pluginID string) (*PluginSummary, error) {
	body, _ := json.Marshal(map[string]string{"plugin_id": pluginID})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/marketplace/upgrade", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(b))
	}

	var result struct {
		Plugin PluginSummary `json:"plugin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result.Plugin, nil
}

// DeployCandidate calls POST /api/plugins/:id/deploy.
// If image is empty, the kernel uses the plugin's current stable image.
func (c *Client) DeployCandidate(id, image string) error {
	payload := map[string]interface{}{"image": image}
	body, _ := json.Marshal(payload)
	resp, err := c.postJSON("/api/plugins/"+id+"/deploy", body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// PromoteCandidate calls POST /api/plugins/:id/promote.
func (c *Client) PromoteCandidate(id string) error {
	return c.doSimple("POST", "/api/plugins/"+id+"/promote")
}

// RollbackCandidate calls POST /api/plugins/:id/rollback.
func (c *Client) RollbackCandidate(id string) error {
	return c.doSimple("POST", "/api/plugins/"+id+"/rollback")
}

// SetPluginConfig calls PUT /api/plugins/:id/config with key-value pairs.
// Keys containing TOKEN, KEY, SECRET, or PASSWORD are marked as secrets.
func (c *Client) SetPluginConfig(id string, values map[string]string) error {
	type configEntry struct {
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	entries := make(map[string]configEntry, len(values))
	for k, v := range values {
		upper := strings.ToUpper(k)
		isSecret := strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "KEY") ||
			strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "PASSWORD")
		entries[k] = configEntry{Value: v, IsSecret: isSecret}
	}
	payload := map[string]any{"config": entries}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("PUT", c.BaseURL+"/api/plugins/"+id+"/config", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update config failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) get(path string) (*http.Response, error) {
	return c.do("GET", path)
}

func (c *Client) post(path string) (*http.Response, error) {
	return c.do("POST", path)
}

func (c *Client) do(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(b))
	}

	return resp, nil
}

func (c *Client) postJSON(path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.BaseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(b))
	}

	return resp, nil
}

// SubmitManifest posts a plugin manifest (as JSON) to the provider catalog.
// The kernel forwards directly to the provider without requiring the plugin to be running.
func (c *Client) SubmitManifest(manifestJSON []byte) error {
	resp, err := c.postJSON("/api/marketplace/manifests", manifestJSON)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) doSimple(method, path string) error {
	resp, err := c.do(method, path)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
