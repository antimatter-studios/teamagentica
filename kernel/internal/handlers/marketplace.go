package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// MarketplaceHandler handles marketplace endpoints: provider management, catalog browsing, install.
type MarketplaceHandler struct {
	db     *gorm.DB
	Events *events.Hub
}

// NewMarketplaceHandler creates a new MarketplaceHandler.
func NewMarketplaceHandler(db *gorm.DB, hub *events.Hub) *MarketplaceHandler {
	return &MarketplaceHandler{db: db, Events: hub}
}

// --- request/response types ---

type addProviderRequest struct {
	Name string `json:"name" binding:"required"`
	URL  string `json:"url" binding:"required"`
}

type installRequest struct {
	PluginID   string `json:"plugin_id" binding:"required"`
	ProviderID *uint  `json:"provider_id"`
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

// --- Provider management ---

// ListProviders handles GET /api/marketplace/providers.
func (h *MarketplaceHandler) ListProviders(c *gin.Context) {
	var providers []models.Provider
	if err := h.db.Find(&providers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch providers"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

// AddProvider handles POST /api/marketplace/providers.
func (h *MarketplaceHandler) AddProvider(c *gin.Context) {
	var req addProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider := models.Provider{
		Name:    req.Name,
		URL:     req.URL,
		Enabled: true,
	}
	if err := h.db.Create(&provider).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add provider"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"provider": provider})
}

// DeleteProvider handles DELETE /api/marketplace/providers/:id.
func (h *MarketplaceHandler) DeleteProvider(c *gin.Context) {
	id := c.Param("id")

	var provider models.Provider
	if err := h.db.First(&provider, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}

	if provider.System {
		c.JSON(http.StatusForbidden, gin.H{"error": "system providers cannot be deleted"})
		return
	}

	if err := h.db.Delete(&provider).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete provider"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "provider deleted"})
}

// --- Catalog browsing ---

// ProviderPlugins handles GET /api/marketplace/providers/:name/plugins.
// Returns plugins from a single named provider.
func (h *MarketplaceHandler) ProviderPlugins(c *gin.Context) {
	name := c.Param("name")

	var provider models.Provider
	if err := h.db.Where("name = ? AND enabled = ?", name, true).First(&provider).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("provider %q not found or disabled", name)})
		return
	}

	entries, groups, err := fetchProviderCatalog(provider.URL, provider.Name, c.Query("q"))
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

	var providers []models.Provider
	if err := h.db.Where("enabled = ?", true).Find(&providers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch providers"})
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
		go func(idx int, p models.Provider) {
			defer wg.Done()
			entries, groups, err := fetchProviderCatalog(p.URL, p.Name, q)
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
func fetchProviderCatalog(providerURL, providerName, query string) ([]CatalogEntry, []CatalogGroup, error) {
	url := providerURL + "/plugins"
	if query != "" {
		url += "?q=" + query
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
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

	var provider models.Provider
	if err := h.db.Where("enabled = ?", true).First(&provider).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no providers configured"})
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(provider.URL+"/manifests", "application/json", bytes.NewReader(body))
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
	if h.db.First(&existing, "id = ?", req.PluginID).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plugin already installed"})
		return
	}

	// Determine which provider to fetch from.
	var provider models.Provider
	if req.ProviderID != nil {
		if err := h.db.First(&provider, "id = ?", *req.ProviderID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
	} else {
		// Use first enabled provider.
		if err := h.db.Where("enabled = ?", true).First(&provider).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no providers configured"})
			return
		}
	}

	// Fetch plugin info from provider.
	entries, _, err := fetchProviderCatalog(provider.URL, provider.Name, req.PluginID)
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

	var allInstalled []models.Plugin
	visited := map[string]bool{}
	plugin, err := h.installPlugin(provider, entry, visited, &allInstalled)
	if err != nil {
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

// installPlugin installs a plugin and its dependencies. Already-installed
// plugins are skipped (use UpgradePlugin to update an installed plugin).
// The visited map prevents infinite dependency loops.
func (h *MarketplaceHandler) installPlugin(provider models.Provider, entry *CatalogEntry, visited map[string]bool, allInstalled *[]models.Plugin) (*models.Plugin, error) {
	if visited[entry.PluginID] {
		return nil, nil
	}
	visited[entry.PluginID] = true

	// If already installed (e.g. as a dependency), skip — use upgrade to update.
	var existing models.Plugin
	if h.db.First(&existing, "id = ?", entry.PluginID).Error == nil {
		return &existing, nil
	}

	// Create plugin record.
	plugin := models.Plugin{
		ID:      entry.PluginID,
		Name:    entry.Name,
		Version: entry.Version,
		Image:   entry.Image,
	}
	if err := h.db.Create(&plugin).Error; err != nil {
		return nil, fmt.Errorf("failed to create plugin %s: %w", plugin.ID, err)
	}

	// Generate service token (10 years).
	expiry := 10 * 365 * 24 * time.Hour
	token, err := auth.GenerateServiceToken(entry.PluginID, []string{"plugins:search"}, expiry)
	if err != nil {
		return nil, fmt.Errorf("failed to generate service token for %s: %w", entry.PluginID, err)
	}
	hash := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", hash)
	capsJSON, _ := json.Marshal([]string{"plugins:search"})
	st := models.ServiceToken{
		Name:         entry.PluginID,
		TokenHash:    tokenHash,
		Capabilities: string(capsJSON),
		IssuedBy:     0,
		ExpiresAt:    time.Now().Add(expiry),
	}
	if err := h.db.Create(&st).Error; err != nil {
		log.Printf("marketplace: failed to create service token for %s: %v", entry.PluginID, err)
	}
	h.db.Model(&plugin).Update("service_token", token)

	// Fetch manifest to get capabilities and dependencies.
	manifest, err := fetchPluginManifest(provider.URL, entry.PluginID)
	if err != nil {
		log.Printf("marketplace: could not fetch manifest for %s: %v (deps won't be resolved)", entry.PluginID, err)
	}

	if manifest != nil {
		updates := map[string]interface{}{}
		if deps, ok := manifest["dependencies"].([]interface{}); ok && len(deps) > 0 {
			var depStrings []string
			for _, d := range deps {
				if s, ok := d.(string); ok {
					depStrings = append(depStrings, s)
				}
			}
			plugin.SetDependencies(depStrings)
			updates["dependencies"] = plugin.Dependencies
		}
		if caps, ok := manifest["capabilities"].([]interface{}); ok && len(caps) > 0 {
			var capStrings []string
			for _, c := range caps {
				if s, ok := c.(string); ok {
					capStrings = append(capStrings, s)
				}
			}
			plugin.SetCapabilities(capStrings)
			updates["capabilities"] = plugin.Capabilities
		}
		if cs, ok := manifest["config_schema"]; ok && cs != nil {
			if b, err := json.Marshal(cs); err == nil {
				plugin.ConfigSchema = models.JSONRawString(b)
				updates["config_schema"] = plugin.ConfigSchema
			}
		}
		if len(updates) > 0 {
			h.db.Model(&plugin).Updates(updates)
		}
	}

	*allInstalled = append(*allInstalled, plugin)

	h.Events.Emit(events.DebugEvent{
		Type:     "install",
		PluginID: entry.PluginID,
		Detail:   fmt.Sprintf("installed from %s (image=%s, version=%s)", provider.Name, entry.Image, entry.Version),
	})

	// Recursively install dependencies.
	for _, cap := range plugin.GetDependencies() {
		depEntry := h.findProviderPluginByCapability(provider, cap)
		if depEntry == nil {
			log.Printf("marketplace: no provider plugin found for capability %q", cap)
			continue
		}
		if _, err := h.installPlugin(provider, depEntry, visited, allInstalled); err != nil {
			log.Printf("marketplace: failed to install dependency %s: %v", depEntry.PluginID, err)
		}
	}

	return &plugin, nil
}

// fetchPluginManifest fetches the full plugin.yaml manifest from a provider.
func fetchPluginManifest(providerURL, pluginID string) (map[string]interface{}, error) {
	url := providerURL + "/plugins/" + pluginID + "/manifest"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
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
// Updates an already-installed plugin's metadata (version, image, capabilities, dependencies)
// from the provider catalog manifest. Fails if the plugin is NOT already installed.
func (h *MarketplaceHandler) UpgradePlugin(c *gin.Context) {
	var req installRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.Plugin
	if err := h.db.First(&existing, "id = ?", req.PluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not installed — use install first"})
		return
	}

	var provider models.Provider
	if req.ProviderID != nil {
		if err := h.db.First(&provider, "id = ?", *req.ProviderID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
	} else {
		if err := h.db.Where("enabled = ?", true).First(&provider).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no providers configured"})
			return
		}
	}

	plugin, err := h.upgradePlugin(provider, req.PluginID, &existing)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "marketplace.upgrade", "plugin:"+req.PluginID,
			fmt.Sprintf(`{"provider":%q,"version":%q}`, provider.Name, plugin.Version),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "plugin upgraded", "plugin": plugin})
}

// upgradePlugin fetches the latest manifest from the provider and updates
// version, image, capabilities, and dependencies in the database.
func (h *MarketplaceHandler) upgradePlugin(provider models.Provider, pluginID string, plugin *models.Plugin) (*models.Plugin, error) {
	manifest, err := fetchPluginManifest(provider.URL, pluginID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest for %s: %w", pluginID, err)
	}

	updates := map[string]interface{}{}

	if v, ok := manifest["version"].(string); ok && v != "" {
		updates["version"] = v
		plugin.Version = v
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

	if len(updates) > 0 {
		if err := h.db.Model(plugin).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update %s: %w", pluginID, err)
		}
	}

	log.Printf("marketplace: upgraded %s (version=%s, caps=%v, deps=%v)", pluginID, plugin.Version, plugin.GetCapabilities(), plugin.GetDependencies())
	return plugin, nil
}

// findProviderPluginByCapability searches the provider catalog for a plugin
// that declares a given capability in its manifest.
func (h *MarketplaceHandler) findProviderPluginByCapability(provider models.Provider, capability string) *CatalogEntry {
	entries, _, err := fetchProviderCatalog(provider.URL, provider.Name, "")
	if err != nil {
		log.Printf("marketplace: failed to fetch catalog for dep resolution: %v", err)
		return nil
	}

	for _, e := range entries {
		manifest, err := fetchPluginManifest(provider.URL, e.PluginID)
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

