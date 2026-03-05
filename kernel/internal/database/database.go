package database

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"

	// Register all migrations via init().
	_ "github.com/antimatter-studios/teamagentica/kernel/migrations"
)

var (
	DB     *gorm.DB
	dbPath string // stored for backup/restore
)

// DBPath returns the database file path used at init.
func DBPath() string { return dbPath }

func Init(path string) {
	dbPath = path
	// SQLite pragmas via DSN: WAL mode, 5s busy timeout, normal sync (safe for WAL).
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"

	var err error
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := migrate.Run(DB); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	seedDefaultProvider(DB)
	seedSystemPlugins(DB)
	migratePluginTokens(DB)
	migrateAliasConfig(DB)

	log.Println("database initialized at", dbPath)
}

// Reinit closes the current connection and reopens the database.
// Called after restoring from backup to pick up the new DB file.
func Reinit() error {
	sqlDB, err := DB.DB()
	if err == nil {
		sqlDB.Close()
	}

	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("reinit database: %w", err)
	}
	log.Println("database reinitialized after restore")
	return nil
}

const defaultProviderName = "Builtin Plugin Provider"

// seedDefaultProvider idempotently creates the default provider record.
// Migrates legacy names (roboslop, plugwerk, teamagentica) to the current name.
func seedDefaultProvider(db *gorm.DB) {
	providerURL := os.Getenv("TEAMAGENTICA_PROVIDER_URL")

	// Remove stale legacy providers that duplicate the builtin one.
	for _, oldName := range []string{"plugwerk", "roboslop"} {
		var old models.Provider
		if db.First(&old, "name = ?", oldName).Error == nil {
			db.Delete(&old)
			log.Printf("database: removed legacy provider %q", oldName)
		}
	}

	// Rename "teamagentica" → current display name if it still has the old name.
	var existing models.Provider
	if db.First(&existing, "name = ?", "teamagentica").Error == nil {
		updates := map[string]interface{}{"name": defaultProviderName}
		if providerURL != "" && existing.URL != providerURL {
			updates["url"] = providerURL
		}
		db.Model(&existing).Updates(updates)
		log.Printf("database: renamed provider 'teamagentica' → %q", defaultProviderName)
		return
	}

	if db.First(&existing, "name = ?", defaultProviderName).Error == nil {
		// Already exists with correct name — update URL if env changed.
		if providerURL != "" && existing.URL != providerURL {
			db.Model(&existing).Update("url", providerURL)
			log.Printf("database: updated default provider url to %s", providerURL)
		}
		return
	}

	if providerURL == "" {
		log.Println("database: TEAMAGENTICA_PROVIDER_URL not set, skipping default provider seed")
		return
	}

	provider := models.Provider{
		Name:    defaultProviderName,
		URL:     providerURL,
		System:  true,
		Enabled: true,
	}
	if err := db.Create(&provider).Error; err != nil {
		log.Printf("database: failed to seed default provider: %v", err)
		return
	}

	log.Printf("database: default provider seeded (url=%s)", providerURL)
}

// systemPlugin defines a plugin that must always exist in the database.
type systemPlugin struct {
	ID           string
	Name         string
	Version      string
	Image        string
	HTTPPort     int
	Capabilities []string
}

// systemPlugins is the list of plugins that are always installed and enabled.
var systemPlugins = []systemPlugin{
	{
		ID:           "builtin-provider",
		Name:         "Builtin Plugin Provider",
		Version:      "1.0.0",
		Image:        "teamagentica-builtin-provider:latest",
		HTTPPort:     8083,
		Capabilities: []string{"marketplace:provider"},
	},
	{
		ID:           "cost-explorer",
		Name:         "Cost Explorer",
		Version:      "1.0.0",
		Image:        "teamagentica-cost-explorer:latest",
		HTTPPort:     8090,
		Capabilities: []string{"system:cost-explorer"},
	},
}

// seedSystemPlugins ensures all system plugins exist in the DB, are enabled,
// and have valid service tokens. Runs idempotently on every boot.
func seedSystemPlugins(db *gorm.DB) {
	// Migrate old plugin IDs.
	for _, oldID := range []string{"teamagentica-provider", "provider"} {
		var old models.Plugin
		if db.First(&old, "id = ?", oldID).Error == nil {
			db.Delete(&old)
			db.Where("name = ? AND revoked = ?", oldID, false).Delete(&models.ServiceToken{})
			log.Printf("database: removed old system plugin %s", oldID)
		}
	}

	for _, sp := range systemPlugins {
		var existing models.Plugin
		if db.First(&existing, "id = ?", sp.ID).Error == nil {
			// Already exists — ensure it's enabled and marked as system.
			updates := map[string]interface{}{
				"enabled": true,
				"system":  true,
				"name":    sp.Name,
			}
			// Update image if changed.
			if existing.Image != sp.Image {
				updates["image"] = sp.Image
			}
			db.Model(&existing).Updates(updates)

			// Ensure service token exists.
			if existing.ServiceToken == "" {
				ensureServiceToken(db, sp.ID)
			}
			continue
		}

		// Create the plugin.
		plugin := models.Plugin{
			ID:       sp.ID,
			Name:     sp.Name,
			Version:  sp.Version,
			Image:    sp.Image,
			HTTPPort: sp.HTTPPort,
			Status:   "stopped",
			Enabled:  true,
			System:   true,
		}
		plugin.SetCapabilities(sp.Capabilities)

		if err := db.Create(&plugin).Error; err != nil {
			log.Printf("database: failed to seed system plugin %s: %v", sp.ID, err)
			continue
		}

		ensureServiceToken(db, sp.ID)
		log.Printf("database: system plugin %s seeded", sp.ID)
	}
}

// ensureServiceToken creates a service token for a plugin if it doesn't have one.
func ensureServiceToken(db *gorm.DB, pluginID string) {
	expiry := 10 * 365 * 24 * time.Hour
	token, err := auth.GenerateServiceToken(pluginID, []string{"plugins:search"}, expiry)
	if err != nil {
		log.Printf("database: failed to generate service token for %s: %v", pluginID, err)
		return
	}

	hash := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", hash)
	capsJSON := `["plugins:search"]`

	// Create token record if not exists.
	var existing models.ServiceToken
	if db.Where("name = ? AND revoked = ?", pluginID, false).First(&existing).Error != nil {
		st := models.ServiceToken{
			Name:         pluginID,
			TokenHash:    tokenHash,
			Capabilities: capsJSON,
			IssuedBy:     0,
			ExpiresAt:    time.Now().Add(expiry),
		}
		if err := db.Create(&st).Error; err != nil {
			log.Printf("database: failed to create service token for %s: %v", pluginID, err)
			return
		}
	}

	// Store token on plugin record.
	db.Model(&models.Plugin{}).Where("id = ?", pluginID).Update("service_token", token)
}

// migratePluginTokens moves TEAMAGENTICA_PLUGIN_TOKEN from plugin_configs to the plugin's service_token field.
func migratePluginTokens(db *gorm.DB) {
	var configs []models.PluginConfig
	db.Where("key = ?", "TEAMAGENTICA_PLUGIN_TOKEN").Find(&configs)
	for _, cfg := range configs {
		db.Model(&models.Plugin{}).Where("id = ? AND (service_token IS NULL OR service_token = '')", cfg.PluginID).
			Update("service_token", cfg.Value)
		db.Delete(&cfg)
	}
	if len(configs) > 0 {
		log.Printf("database: migrated %d plugin token(s) from config to plugin record", len(configs))
	}
}

// migrateAliasConfig converts old PLUGIN_ALIAS/PLUGIN_ALIAS_TARGET config entries
// into the new PLUGIN_ALIASES JSON array format and sets PluginID on matching aliases.
func migrateAliasConfig(db *gorm.DB) {
	var aliasConfigs []models.PluginConfig
	db.Where("key = ?", "PLUGIN_ALIAS").Find(&aliasConfigs)
	if len(aliasConfigs) == 0 {
		return
	}

	migrated := 0
	for _, ac := range aliasConfigs {
		pluginID := ac.PluginID
		aliasName := ac.Value
		if aliasName == "" {
			continue
		}

		// Find the target (defaults to plugin ID).
		aliasTarget := pluginID
		var targetCfg models.PluginConfig
		if db.Where("plugin_id = ? AND key = ?", pluginID, "PLUGIN_ALIAS_TARGET").First(&targetCfg).Error == nil {
			if targetCfg.Value != "" {
				aliasTarget = targetCfg.Value
			}
		}

		// Build PLUGIN_ALIASES JSON.
		entry := []map[string]string{{"name": aliasName, "target": aliasTarget}}
		aliasJSON, _ := json.Marshal(entry)

		// Check if PLUGIN_ALIASES already exists.
		var existing models.PluginConfig
		if db.Where("plugin_id = ? AND key = ?", pluginID, "PLUGIN_ALIASES").First(&existing).Error != nil {
			db.Create(&models.PluginConfig{
				PluginID: pluginID,
				Key:      "PLUGIN_ALIASES",
				Value:    string(aliasJSON),
			})
		}

		// Set PluginID on matching alias in the aliases table.
		db.Model(&models.Alias{}).Where("name = ? AND plugin_id = ''", aliasName).
			Update("plugin_id", pluginID)

		// Delete old config entries.
		db.Where("plugin_id = ? AND key = ?", pluginID, "PLUGIN_ALIAS").Delete(&models.PluginConfig{})
		db.Where("plugin_id = ? AND key = ?", pluginID, "PLUGIN_ALIAS_TARGET").Delete(&models.PluginConfig{})

		migrated++
	}

	if migrated > 0 {
		log.Printf("database: migrated %d plugin(s) from PLUGIN_ALIAS to PLUGIN_ALIASES", migrated)
	}
}

