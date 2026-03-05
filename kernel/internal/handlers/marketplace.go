package handlers

import (
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

// CatalogPricingEntry holds default pricing for a model (from provider catalog).
type CatalogPricingEntry struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
	CachedPer1M float64 `json:"cached_per_1m"`
	PerRequest  float64 `json:"per_request"`
	Currency    string  `json:"currency"`
}

// CatalogEntry is the shape returned by provider /plugins endpoints.
type CatalogEntry struct {
	PluginID       string                 `json:"plugin_id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Version        string                 `json:"version"`
	Image          string                 `json:"image"`
	Author         string                 `json:"author"`
	Tags           []string               `json:"tags"`
	ConfigSchema   map[string]interface{} `json:"config_schema,omitempty"`
	DefaultPricing []CatalogPricingEntry  `json:"default_pricing,omitempty"`
	Provider       string                 `json:"provider,omitempty"`
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
		err     error
	}

	var wg sync.WaitGroup
	results := make([]providerResult, len(providers))

	for i, prov := range providers {
		wg.Add(1)
		go func(idx int, p models.Provider) {
			defer wg.Done()
			entries, err := fetchProviderCatalog(p.URL, p.Name, q)
			results[idx] = providerResult{entries: entries, err: err}
		}(i, prov)
	}
	wg.Wait()

	var all []CatalogEntry
	for _, r := range results {
		if r.err != nil {
			log.Printf("marketplace: provider fetch error: %v", r.err)
			continue
		}
		all = append(all, r.entries...)
	}

	if all == nil {
		all = []CatalogEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"plugins": all})
}

// fetchProviderCatalog fetches the plugin catalog from a provider URL.
func fetchProviderCatalog(providerURL, providerName, query string) ([]CatalogEntry, error) {
	url := providerURL + "/plugins"
	if query != "" {
		url += "?q=" + query
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", providerURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider %s returned status %d", providerURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body from %s: %w", providerURL, err)
	}

	var result struct {
		Plugins []CatalogEntry `json:"plugins"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response from %s: %w", providerURL, err)
	}

	// Tag each entry with the provider name.
	for i := range result.Plugins {
		result.Plugins[i].Provider = providerName
	}

	return result.Plugins, nil
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
	entries, err := fetchProviderCatalog(provider.URL, provider.Name, req.PluginID)
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

	// Create plugin record.
	plugin := models.Plugin{
		ID:      entry.PluginID,
		Name:    entry.Name,
		Version: entry.Version,
		Image:   entry.Image,
	}
	if entry.Tags != nil {
		plugin.SetCapabilities(entry.Tags)
	}
	if entry.ConfigSchema != nil {
		schemaJSON, _ := json.Marshal(entry.ConfigSchema)
		plugin.ConfigSchema = models.JSONRawString(schemaJSON)
	}

	if err := h.db.Create(&plugin).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create plugin record"})
		return
	}

	// Generate service token (10 years).
	expiry := 10 * 365 * 24 * time.Hour
	token, err := auth.GenerateServiceToken(entry.PluginID, []string{"plugins:search"}, expiry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate service token"})
		return
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

	// Store token on the plugin record (internal, not user config).
	h.db.Model(&plugin).Update("service_token", token)

	// Seed default pricing from catalog (only for models not yet priced).
	if len(entry.DefaultPricing) > 0 {
		for _, p := range entry.DefaultPricing {
			var count int64
			h.db.Model(&models.ModelPrice{}).
				Where("provider = ? AND model = ?", p.Provider, p.Model).Count(&count)
			if count == 0 {
				if _, err := SavePriceRecord(h.db, p.Provider, p.Model, p.InputPer1M, p.OutputPer1M, p.CachedPer1M, p.PerRequest, p.Currency); err != nil {
					log.Printf("marketplace: failed to seed price for %s/%s: %v", p.Provider, p.Model, err)
				}
			}
		}
		log.Printf("marketplace: seeded default pricing for %s (%d models)", entry.PluginID, len(entry.DefaultPricing))
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "install",
		PluginID: entry.PluginID,
		Detail:   fmt.Sprintf("installed from %s (image=%s, version=%s)", provider.Name, entry.Image, entry.Version),
	})

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "marketplace.install", "plugin:"+entry.PluginID,
			fmt.Sprintf(`{"provider":%q,"version":%q}`, provider.Name, entry.Version),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "plugin installed",
		"plugin":  plugin,
	})
}
