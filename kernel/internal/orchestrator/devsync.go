package orchestrator

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// catalogEntry mirrors the shape returned by provider /plugins endpoints.
type catalogEntry struct {
	PluginID     string                 `json:"plugin_id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Version      string                 `json:"version"`
	Image        string                 `json:"image"`
	Tags         []string               `json:"tags"`
	ConfigSchema map[string]interface{} `json:"config_schema,omitempty"`
}

// DevSyncCatalog fetches the catalog from the builtin provider and installs any
// plugins that exist in the catalog but not in the database. This is only intended
// for dev mode so new plugins appear automatically without manual marketplace install.
func (o *Orchestrator) DevSyncCatalog() {
	if !o.config.DevMode {
		return
	}

	providerURL := o.config.ProviderURL
	if providerURL == "" {
		log.Println("devsync: no TEAMAGENTICA_PROVIDER_URL set, skipping catalog sync")
		return
	}

	// Fetch full catalog from the builtin provider.
	url := providerURL + "/plugins"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("devsync: failed to fetch catalog from %s: %v", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("devsync: provider returned status %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("devsync: failed to read catalog body: %v", err)
		return
	}

	var result struct {
		Plugins []catalogEntry `json:"plugins"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("devsync: failed to parse catalog: %v", err)
		return
	}

	installed := 0
	for _, entry := range result.Plugins {
		var existing models.Plugin
		if o.db.First(&existing, "id = ?", entry.PluginID).Error == nil {
			continue // already installed
		}

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

		if err := o.db.Create(&plugin).Error; err != nil {
			log.Printf("devsync: failed to create plugin %s: %v", entry.PluginID, err)
			continue
		}

		// Generate service token.
		expiry := 10 * 365 * 24 * time.Hour
		token, err := auth.GenerateServiceToken(entry.PluginID, []string{"plugins:search"}, expiry)
		if err != nil {
			log.Printf("devsync: failed to generate token for %s: %v", entry.PluginID, err)
			continue
		}

		hash := sha256.Sum256([]byte(token))
		tokenHash := fmt.Sprintf("%x", hash)
		capsJSON := `["plugins:search"]`

		st := models.ServiceToken{
			Name:         entry.PluginID,
			TokenHash:    tokenHash,
			Capabilities: capsJSON,
			IssuedBy:     0,
			ExpiresAt:    time.Now().Add(expiry),
		}
		o.db.Create(&st)
		o.db.Model(&plugin).Update("service_token", token)

		installed++
		log.Printf("devsync: auto-installed catalog plugin %s", entry.PluginID)
		o.emit("devsync", entry.PluginID, "auto-installed from catalog")
	}

	if installed > 0 {
		log.Printf("devsync: installed %d new plugin(s) from catalog", installed)
	}
}
