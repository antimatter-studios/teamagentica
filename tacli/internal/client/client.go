package client

import (
	"bufio"
	"context"
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Image        string   `json:"image"`
	Status       string   `json:"status"`
	Enabled      bool     `json:"enabled"`
	System       bool     `json:"system"`
	Capabilities []string `json:"capabilities"`
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

// SetPluginDevMode calls POST /api/plugins/:id/dev-mode and toggles the
// kernel into using each container's dev_image + dev_bind_mounts (or back).
// The kernel restarts the plugin's pod so the new variant takes effect.
func (c *Client) SetPluginDevMode(id string, enabled bool) error {
	body := []byte(`{"enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`)
	_, err := c.postJSON("/api/plugins/"+id+"/dev-mode", body)
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

// DeleteManifest removes all versions of a plugin from the provider catalog.
func (c *Client) DeleteManifest(pluginID string) error {
	return c.doSimple("DELETE", "/api/marketplace/manifests/"+pluginID)
}

func (c *Client) doSimple(method, path string) error {
	resp, err := c.do(method, path)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ── typed config ──────────────────────────────────────────────────────────────

// ConfigItem is a single config field returned by GET /api/plugins/:id/config.
type ConfigItem struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
	Default  string `json:"default,omitempty"`
	Label    string `json:"label,omitempty"`
	Required bool   `json:"required,omitempty"`
	ReadOnly bool   `json:"readonly,omitempty"`
}

// PluginConfigItems calls GET /api/plugins/:id/config and returns typed items.
func (c *Client) PluginConfigItems(id string) ([]ConfigItem, error) {
	resp, err := c.get("/api/plugins/" + id + "/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var wrapper struct {
		Config []ConfigItem `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return wrapper.Config, nil
}

// ConfigSchemaField describes a single field from the plugin's config schema.
type ConfigSchemaField struct {
	Type        string      `json:"type"`
	Label       string      `json:"label"`
	Required    bool        `json:"required,omitempty"`
	Secret      bool        `json:"secret,omitempty"`
	ReadOnly    bool        `json:"readonly,omitempty"`
	Default     string      `json:"default,omitempty"`
	Options     []string    `json:"options,omitempty"`
	Dynamic     bool        `json:"dynamic,omitempty"`
	HelpText    string      `json:"help_text,omitempty"`
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`
	Order       int         `json:"order,omitempty"`
}

// VisibleWhen describes a conditional visibility rule.
type VisibleWhen struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// PluginConfigSchema calls GET /api/plugins/:id/schema/config and returns the schema.
func (c *Client) PluginConfigSchema(id string) (map[string]ConfigSchemaField, error) {
	resp, err := c.get("/api/plugins/" + id + "/schema/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var schema map[string]ConfigSchemaField
	if err := json.NewDecoder(resp.Body).Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return schema, nil
}

// ── OAuth device-code flow ───────────────────────────────────────────────────

// DeviceCodeResponse is returned by POST /api/route/:id/auth/device-code.
type DeviceCodeResponse struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

// OAuthDeviceCode initiates the device-code flow for a plugin.
// Uses a 45s timeout to allow the plugin to start its auth subprocess and
// return the OAuth URL (which can take up to ~30s).
func (c *Client) OAuthDeviceCode(pluginID string) (*DeviceCodeResponse, error) {
	slowClient := &http.Client{Timeout: 45 * time.Second}
	req, err := http.NewRequest("POST", c.BaseURL+"/api/route/"+pluginID+"/auth/device-code", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := slowClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(body))
	}
	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// OAuthSubmitCode delivers the authorization code to the plugin's login process
// and waits for authentication to complete. Returns whether auth succeeded.
func (c *Client) OAuthSubmitCode(pluginID, code string) (bool, error) {
	slowClient := &http.Client{Timeout: 45 * time.Second}
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := http.NewRequest("POST", c.BaseURL+"/api/route/"+pluginID+"/auth/submit-code", strings.NewReader(string(body)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := slowClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("submit-code failed (%d): %s", resp.StatusCode, string(b))
	}
	var result struct {
		Authenticated bool `json:"authenticated"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Authenticated, nil
}

// OAuthStatusFull returns authenticated + login_in_progress for a plugin.
func (c *Client) OAuthStatusFull(pluginID string) (authenticated bool, loginInProgress bool, err error) {
	resp, err := c.get("/api/route/" + pluginID + "/auth/status")
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()
	var result struct {
		Authenticated   bool `json:"authenticated"`
		LoginInProgress bool `json:"login_in_progress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, false, fmt.Errorf("decode: %w", err)
	}
	return result.Authenticated, result.LoginInProgress, nil
}

// OAuthPoll checks whether a generic device-code flow has completed (e.g. OpenAI).
func (c *Client) OAuthPoll(pluginID string) (bool, error) {
	resp, err := c.post("/api/route/" + pluginID + "/auth/poll")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode: %w", err)
	}
	return result.Authenticated, nil
}

// OAuthStatus checks current auth status for a plugin.
func (c *Client) OAuthStatus(pluginID string) (bool, error) {
	resp, err := c.get("/api/route/" + pluginID + "/auth/status")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode: %w", err)
	}
	return result.Authenticated, nil
}

// ── plugin detail ─────────────────────────────────────────────────────────────

// PluginDetail is the full plugin record returned by GET /api/plugins/:id.
type PluginDetail struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Group            string   `json:"group"`
	Version          string   `json:"version"`
	Image            string   `json:"image"`
	Status           string   `json:"status"`
	Enabled          bool     `json:"enabled"`
	System           bool     `json:"system"`
	Host             string   `json:"host"`
	Capabilities     []string `json:"capabilities"`
	Dependencies     []string `json:"dependencies"`
	CandidateImage   string   `json:"candidate_image"`
	CandidateVersion string   `json:"candidate_version"`
}

// GetPlugin calls GET /api/plugins/:id and returns the full plugin record.
func (c *Client) GetPlugin(id string) (*PluginDetail, error) {
	resp, err := c.get("/api/plugins/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var wrapper struct {
		Plugin PluginDetail `json:"plugin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &wrapper.Plugin, nil
}

// GetPluginLogs calls GET /api/plugins/:id/logs and returns plain-text log output.
func (c *Client) GetPluginLogs(id string, tail int) (string, error) {
	resp, err := c.get(fmt.Sprintf("/api/plugins/%s/logs?tail=%d", id, tail))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(b), nil
}

// GetKernelLogs calls GET /api/kernel/logs and returns plain-text log output.
func (c *Client) GetKernelLogs(tail int) (string, error) {
	resp, err := c.get(fmt.Sprintf("/api/kernel/logs?tail=%d", tail))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(b), nil
}

func (c *Client) GetUILogs(tail int) (string, error) {
	resp, err := c.get(fmt.Sprintf("/api/kernel/ui/logs?tail=%d", tail))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(b), nil
}

// ── SSE event streaming ───────────────────────────────────────────────────────

// SSEEvent is a parsed server-sent event from the kernel debug stream.
type SSEEvent struct {
	Channel string
	Data    json.RawMessage
}

// StreamEvents connects to GET /api/debug/events and sends parsed events to ch
// until ctx is cancelled or the connection drops.
func (c *Client) StreamEvents(ctx context.Context, ch chan<- SSEEvent) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/debug/events", nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	// Use a client with no timeout for the streaming connection.
	sseClient := &http.Client{}
	resp, err := sseClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var eventType, data string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && eventType != "" && data != "":
			select {
			case ch <- SSEEvent{Channel: eventType, Data: json.RawMessage(data)}:
			case <-ctx.Done():
				return nil
			}
			eventType, data = "", ""
		}
	}
	return scanner.Err()
}
