package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// MarketplaceHandler handles marketplace endpoints: provider management, catalog browsing, install.
type MarketplaceHandler struct {
	Events     *events.Hub
	httpClient *http.Client // mTLS-aware client for calling provider plugins
}

// NewMarketplaceHandler creates a new MarketplaceHandler.
func NewMarketplaceHandler(hub *events.Hub, client *http.Client) *MarketplaceHandler {
	return &MarketplaceHandler{Events: hub, httpClient: client}
}

func (h *MarketplaceHandler) db() *gorm.DB { return database.Get() }

// --- request/response types ---

type addProviderRequest struct {
	Name string `json:"name" binding:"required"`
	URL  string `json:"url" binding:"required"`
}

type installRequest struct {
	PluginID   string `json:"plugin_id" binding:"required"`
	ProviderID *uint  `json:"provider_id"`
}

// providerInfo is a local representation of a provider record fetched from plugin-provider.
type providerInfo struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	System  bool   `json:"system"`
	Enabled bool   `json:"enabled"`
}

// providerPluginURL returns the base URL of the system-teamagentica-plugin-provider plugin.
func (h *MarketplaceHandler) providerPluginURL() (string, error) {
	var plugin models.Plugin
	if err := h.db().First(&plugin, "id = ?", "system-teamagentica-plugin-provider").Error; err != nil {
		return "", fmt.Errorf("plugin-provider not found: %w", err)
	}
	return fmt.Sprintf("https://%s:%d", plugin.Host, plugin.HTTPPort), nil
}

// fetchProviders fetches the provider list from the plugin-provider plugin.
func (h *MarketplaceHandler) fetchProviders() ([]providerInfo, error) {
	baseURL, err := h.providerPluginURL()
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Get(baseURL + "/providers")
	if err != nil {
		return nil, fmt.Errorf("fetch providers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin-provider returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read providers body: %w", err)
	}
	var result struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse providers: %w", err)
	}
	return result.Providers, nil
}

// fetchEnabledProviders returns only enabled providers from plugin-provider.
func (h *MarketplaceHandler) fetchEnabledProviders() ([]providerInfo, error) {
	all, err := h.fetchProviders()
	if err != nil {
		return nil, err
	}
	var enabled []providerInfo
	for _, p := range all {
		if p.Enabled {
			enabled = append(enabled, p)
		}
	}
	return enabled, nil
}

// fetchFirstEnabledProvider returns the first enabled provider or an error.
func (h *MarketplaceHandler) fetchFirstEnabledProvider() (providerInfo, error) {
	providers, err := h.fetchEnabledProviders()
	if err != nil {
		return providerInfo{}, err
	}
	if len(providers) == 0 {
		return providerInfo{}, fmt.Errorf("no providers configured")
	}
	return providers[0], nil
}

// fetchProviderByID returns a specific provider by ID.
func (h *MarketplaceHandler) fetchProviderByID(id uint) (providerInfo, error) {
	all, err := h.fetchProviders()
	if err != nil {
		return providerInfo{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return providerInfo{}, fmt.Errorf("provider not found")
}

// fetchProviderByName returns a specific enabled provider by name.
func (h *MarketplaceHandler) fetchProviderByName(name string) (providerInfo, error) {
	all, err := h.fetchEnabledProviders()
	if err != nil {
		return providerInfo{}, err
	}
	for _, p := range all {
		if p.Name == name {
			return p, nil
		}
	}
	return providerInfo{}, fmt.Errorf("provider %q not found or disabled", name)
}

// CatalogEntry is the shape returned by provider /plugins endpoints.
// This is a reference index only — enough for the marketplace UI to display
// what's installable. All plugin data comes from the plugin itself after boot.
type CatalogEntry struct {
	PluginID    string   `json:"plugin_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Group       string   `json:"group,omitempty"`
	Version     string   `json:"version"`
	Image       string   `json:"image"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Provider    string   `json:"provider,omitempty"`
}

// CatalogGroup holds display metadata for a plugin group.
type CatalogGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

// validateProviderURL checks that a provider URL is safe to fetch from.
// It rejects non-HTTP schemes, localhost, and private/link-local IP ranges.
func validateProviderURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL has no hostname")
	}

	if strings.EqualFold(hostname, "localhost") {
		return fmt.Errorf("localhost is not allowed as a provider URL")
	}

	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %q: %w", hostname, err)
	}

	privateCIDRs := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
	}
	var privateNets []*net.IPNet
	for _, cidr := range privateCIDRs {
		_, n, _ := net.ParseCIDR(cidr)
		privateNets = append(privateNets, n)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		for _, n := range privateNets {
			if n.Contains(ip) {
				return fmt.Errorf("provider URL resolves to private/reserved address %s", ipStr)
			}
		}
	}

	return nil
}

// --- Provider management (proxied to plugin-provider) ---

// ListProviders handles GET /api/marketplace/providers.
func (h *MarketplaceHandler) ListProviders(c *gin.Context) {
	baseURL, err := h.providerPluginURL()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "plugin-provider unavailable: " + err.Error()})
		return
	}
	resp, err := h.httpClient.Get(baseURL + "/providers")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach plugin-provider: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}

// AddProvider handles POST /api/marketplace/providers.
func (h *MarketplaceHandler) AddProvider(c *gin.Context) {
	var req addProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateProviderURL(req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider URL: " + err.Error()})
		return
	}

	baseURL, err := h.providerPluginURL()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "plugin-provider unavailable: " + err.Error()})
		return
	}

	reqBody, _ := json.Marshal(req)
	resp, err := h.httpClient.Post(baseURL+"/providers", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach plugin-provider: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}

// DeleteProvider handles DELETE /api/marketplace/providers/:id.
func (h *MarketplaceHandler) DeleteProvider(c *gin.Context) {
	id := c.Param("id")

	baseURL, err := h.providerPluginURL()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "plugin-provider unavailable: " + err.Error()})
		return
	}

	delReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodDelete, baseURL+"/providers/"+id, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}
	resp, err := h.httpClient.Do(delReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach plugin-provider: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}

// --- Catalog browsing ---

// ProviderPlugins handles GET /api/marketplace/providers/:name/plugins.
// Returns plugins from a single named provider.
func (h *MarketplaceHandler) ProviderPlugins(c *gin.Context) {
	name := c.Param("name")

	provider, err := h.fetchProviderByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	entries, groups, err := fetchProviderCatalog(h.httpClient, provider.URL, provider.Name, c.Query("q"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch from provider: " + err.Error()})
		return
	}

	if entries == nil {
		entries = []CatalogEntry{}
	}
	if groups == nil {
		groups = []CatalogGroup{}
	}

	c.JSON(http.StatusOK, gin.H{"plugins": entries, "groups": groups, "provider": provider.Name})
}

// BrowsePlugins handles GET /api/marketplace/plugins?q=...
func (h *MarketplaceHandler) BrowsePlugins(c *gin.Context) {
	q := c.Query("q")

	providers, err := h.fetchEnabledProviders()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch providers: " + err.Error()})
		return
	}

	if len(providers) == 0 {
		c.JSON(http.StatusOK, gin.H{"plugins": []CatalogEntry{}})
		return
	}

	type providerResult struct {
		entries []CatalogEntry
		groups  []CatalogGroup
		err     error
	}

	var wg sync.WaitGroup
	results := make([]providerResult, len(providers))

	for i, prov := range providers {
		wg.Add(1)
		go func(idx int, p providerInfo) {
			defer wg.Done()
			entries, groups, err := fetchProviderCatalog(h.httpClient, p.URL, p.Name, q)
			results[idx] = providerResult{entries: entries, groups: groups, err: err}
		}(i, prov)
	}
	wg.Wait()

	var all []CatalogEntry
	groupsSeen := map[string]bool{}
	var allGroups []CatalogGroup
	var fetchErrors []string
	for i, r := range results {
		if r.err != nil {
			log.Printf("marketplace: provider fetch error: %v", r.err)
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %v", providers[i].Name, r.err))
			continue
		}
		all = append(all, r.entries...)
		for _, g := range r.groups {
			if !groupsSeen[g.ID] {
				groupsSeen[g.ID] = true
				allGroups = append(allGroups, g)
			}
		}
	}

	if all == nil {
		all = []CatalogEntry{}
	}
	if allGroups == nil {
		allGroups = []CatalogGroup{}
	}

	resp := gin.H{"plugins": all, "groups": allGroups}
	if len(fetchErrors) > 0 {
		resp["errors"] = fetchErrors
	}
	c.JSON(http.StatusOK, resp)
}

// fetchProviderCatalog fetches the plugin catalog from a provider URL.
func fetchProviderCatalog(client *http.Client, providerURL, providerName, query string) ([]CatalogEntry, []CatalogGroup, error) {
	fetchURL := providerURL + "/plugins"
	if query != "" {
		fetchURL += "?q=" + url.QueryEscape(query)
	}

	resp, err := client.Get(fetchURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", providerURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("provider %s returned status %d", providerURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body from %s: %w", providerURL, err)
	}

	var result struct {
		Plugins []CatalogEntry `json:"plugins"`
		Groups  []CatalogGroup `json:"groups"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, fmt.Errorf("parse response from %s: %w", providerURL, err)
	}

	// Tag each entry with the provider name.
	for i := range result.Plugins {
		result.Plugins[i].Provider = providerName
	}

	return result.Plugins, result.Groups, nil
}

// --- Manifest submission ---

// SubmitManifest handles POST /api/marketplace/manifests.
// Forwards the manifest directly to the first enabled provider's /manifests endpoint.
// Does not require the provider plugin to be registered or running.
func (h *MarketplaceHandler) SubmitManifest(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	provider, err := h.fetchFirstEnabledProvider()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	client := h.httpClient
	resp, err := client.Post(provider.URL+"/manifests", "application/json", bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach provider: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", respBody)
}

// DeleteManifest handles DELETE /api/marketplace/manifests/:id.
// Forwards the delete request to the first enabled provider.
func (h *MarketplaceHandler) DeleteManifest(c *gin.Context) {
	pluginID := c.Param("id")

	provider, err := h.fetchFirstEnabledProvider()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodDelete, provider.URL+"/plugins/"+pluginID, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach provider: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", respBody)
}

// --- Install ---

// InstallPlugin handles POST /api/marketplace/install.
func (h *MarketplaceHandler) InstallPlugin(c *gin.Context) {
	var req installRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if plugin already installed.
	var existing models.Plugin
	if h.db().First(&existing, "id = ?", req.PluginID).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plugin already installed"})
		return
	}

	// Determine which provider to fetch from.
	var provider providerInfo
	if req.ProviderID != nil {
		var err error
		provider, err = h.fetchProviderByID(*req.ProviderID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
	} else {
		var err error
		provider, err = h.fetchFirstEnabledProvider()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	}

	// Fetch plugin info from provider.
	entries, _, err := fetchProviderCatalog(h.httpClient, provider.URL, provider.Name, req.PluginID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch from provider: " + err.Error()})
		return
	}

	var entry *CatalogEntry
	for i := range entries {
		if entries[i].PluginID == req.PluginID {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found in provider catalog"})
		return
	}

	plugin, err := h.bootstrapPlugin(entry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var allInstalled []models.Plugin
	visited := map[string]bool{}
	if err := h.syncPlugin(provider, plugin, visited, &allInstalled, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "marketplace.install", "plugin:"+entry.PluginID,
			fmt.Sprintf(`{"provider":%q,"version":%q}`, provider.Name, entry.Version),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":   "plugin installed",
		"plugin":    plugin,
		"installed": allInstalled,
	})
}

// bootstrapPlugin creates a new plugin DB record.
// Called only for first-time installs, before syncPlugin.
func (h *MarketplaceHandler) bootstrapPlugin(entry *CatalogEntry) (*models.Plugin, error) {
	plugin := models.Plugin{
		ID:      entry.PluginID,
		Name:    stripHTMLTags(entry.Name),
		Version: entry.Version,
		Image:   entry.Image,
	}
	if err := h.db().Create(&plugin).Error; err != nil {
		return nil, fmt.Errorf("failed to create plugin %s: %w", plugin.ID, err)
	}

	return &plugin, nil
}

// syncPlugin fetches the latest manifest from the provider and applies it to
// the plugin record. When installDeps is true (fresh install), missing
// dependencies are auto-installed from the catalog. When false (upgrade),
// only already-installed dependencies are synced — no new installs.
func (h *MarketplaceHandler) syncPlugin(provider providerInfo, plugin *models.Plugin, visited map[string]bool, allInstalled *[]models.Plugin, installDeps bool) error {
	if visited[plugin.ID] {
		return nil
	}
	visited[plugin.ID] = true

	manifest, err := fetchPluginManifest(h.httpClient, provider.URL, plugin.ID)
	if err != nil {
		return fmt.Errorf("could not fetch manifest for %s: %w", plugin.ID, err)
	}
	if err := applyManifest(plugin, manifest, h.db()); err != nil {
		return err
	}

	*allInstalled = append(*allInstalled, *plugin)

	// Recursively ensure dependencies exist and are synced.
	for _, cap := range plugin.GetDependencies() {
		var dep models.Plugin
		if installDeps {
			// Install path: look up capability in catalog and auto-install if missing.
			depEntry := h.findProviderPluginByCapability(provider, cap)
			if depEntry == nil {
				log.Printf("marketplace: no provider plugin found for capability %q", cap)
				continue
			}
			if h.db().First(&dep, "id = ?", depEntry.PluginID).Error != nil {
				bootstrapped, err := h.bootstrapPlugin(depEntry)
				if err != nil {
					log.Printf("marketplace: failed to bootstrap dependency %s: %v", depEntry.PluginID, err)
					continue
				}
				dep = *bootstrapped
			}
		} else {
			// Upgrade path: only sync already-installed plugins that provide this capability.
			if err := h.db().Where("capabilities LIKE ?", "%"+cap+"%").First(&dep).Error; err != nil {
				log.Printf("marketplace: dependency %q not installed, skipping (upgrade does not auto-install)", cap)
				continue
			}
		}

		if err := h.syncPlugin(provider, &dep, visited, allInstalled, installDeps); err != nil {
			log.Printf("marketplace: failed to sync dependency %s: %v", dep.ID, err)
		}
	}

	return nil
}

// fetchPluginManifest fetches the full plugin.yaml manifest from a provider.
func fetchPluginManifest(client *http.Client, providerURL, pluginID string) (map[string]interface{}, error) {
	manifestURL := providerURL + "/plugins/" + pluginID + "/manifest"
	resp, err := client.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest body: %w", err)
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return manifest, nil
}

// UpgradePlugin handles POST /api/marketplace/upgrade.
// Updates an already-installed plugin's metadata from the provider manifest.
// Uses the same ensurePlugin path as install — existing plugins get updated in place.
func (h *MarketplaceHandler) UpgradePlugin(c *gin.Context) {
	var req installRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.Plugin
	if err := h.db().First(&existing, "id = ?", req.PluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not installed — use install first"})
		return
	}

	var provider providerInfo
	if req.ProviderID != nil {
		var err error
		provider, err = h.fetchProviderByID(*req.ProviderID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
	} else {
		var err error
		provider, err = h.fetchFirstEnabledProvider()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	}

	var allInstalled []models.Plugin
	visited := map[string]bool{}
	if err := h.syncPlugin(provider, &existing, visited, &allInstalled, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "marketplace.upgrade", "plugin:"+req.PluginID,
			fmt.Sprintf(`{"provider":%q,"version":%q}`, provider.Name, existing.Version),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "plugin upgraded", "plugin": existing})
}

// applyManifest applies manifest fields (version, image, capabilities, dependencies,
// config_schema) to a plugin model and persists any changes to the database.
func applyManifest(plugin *models.Plugin, manifest map[string]interface{}, db *gorm.DB) error {
	updates := map[string]interface{}{}

	if v, ok := manifest["version"].(string); ok && v != "" {
		updates["version"] = v
		plugin.Version = v
	}
	if name, ok := manifest["name"].(string); ok && name != "" {
		updates["name"] = name
		plugin.Name = name
	}
	if img, ok := manifest["image"].(string); ok && img != "" {
		updates["image"] = img
		plugin.Image = img
	}
	if caps, ok := manifest["capabilities"].([]interface{}); ok {
		var capStrings []string
		for _, c := range caps {
			if s, ok := c.(string); ok {
				capStrings = append(capStrings, s)
			}
		}
		plugin.SetCapabilities(capStrings)
		updates["capabilities"] = plugin.Capabilities
	}
	if deps, ok := manifest["dependencies"].([]interface{}); ok {
		var depStrings []string
		for _, d := range deps {
			if s, ok := d.(string); ok {
				depStrings = append(depStrings, s)
			}
		}
		plugin.SetDependencies(depStrings)
		updates["dependencies"] = plugin.Dependencies
	}
	if cs, ok := manifest["config_schema"]; ok && cs != nil {
		if b, err := json.Marshal(cs); err == nil {
			plugin.ConfigSchema = models.JSONRawString(b)
			updates["config_schema"] = plugin.ConfigSchema
		}
	}
	if sd, ok := manifest["shared_disks"]; ok && sd != nil {
		if b, err := json.Marshal(sd); err == nil {
			plugin.SharedDisks = models.JSONRawString(b)
			updates["shared_disks"] = plugin.SharedDisks
		}
	}
	if rs, ok := manifest["requested_scopes"].([]interface{}); ok {
		var scopeStrings []string
		for _, s := range rs {
			if str, ok := s.(string); ok {
				scopeStrings = append(scopeStrings, str)
			}
		}
		plugin.SetRequestedScopes(scopeStrings)
		updates["requested_scopes"] = plugin.RequestedScopes
	}

	if len(updates) > 0 {
		if err := db.Model(plugin).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to apply manifest for %s: %w", plugin.ID, err)
		}
	}
	return nil
}

// findProviderPluginByCapability searches the provider catalog for a plugin
// that declares a given capability in its manifest.
func (h *MarketplaceHandler) findProviderPluginByCapability(provider providerInfo, capability string) *CatalogEntry {
	entries, _, err := fetchProviderCatalog(h.httpClient, provider.URL, provider.Name, "")
	if err != nil {
		log.Printf("marketplace: failed to fetch catalog for dep resolution: %v", err)
		return nil
	}

	for _, e := range entries {
		manifest, err := fetchPluginManifest(h.httpClient, provider.URL, e.PluginID)
		if err != nil {
			continue
		}
		caps, ok := manifest["capabilities"].([]interface{})
		if !ok {
			continue
		}
		for _, c := range caps {
			if s, ok := c.(string); ok && s == capability {
				return &e
			}
		}
	}
	return nil
}

