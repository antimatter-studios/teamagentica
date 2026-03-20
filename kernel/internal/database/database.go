package database

import (
	"crypto/sha256"
	"fmt"
	"log"
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

	loadCachedJWTSecret(DB)
	seedDefaultProvider(DB)
	seedSystemPlugins(DB)

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

// seedDefaultProvider idempotently creates the default marketplace provider
// record, deriving the URL from the system-teamagentica-plugin-provider system plugin definition.
func seedDefaultProvider(db *gorm.DB) {
	var sp *systemPlugin
	for i := range systemPlugins {
		if systemPlugins[i].ID == "system-teamagentica-plugin-provider" {
			sp = &systemPlugins[i]
			break
		}
	}
	if sp == nil {
		log.Println("database: no system-teamagentica-plugin-provider in systemPlugins, skipping default provider seed")
		return
	}

	providerURL := fmt.Sprintf("https://teamagentica-plugin-%s:%d", sp.ID, sp.HTTPPort)

	var existing models.Provider
	if db.First(&existing, "name = ?", sp.Name).Error == nil {
		if existing.URL != providerURL {
			db.Model(&existing).Update("url", providerURL)
			log.Printf("database: updated default provider url to %s", providerURL)
		}
		return
	}

	provider := models.Provider{
		Name:    sp.Name,
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
		ID:           "system-teamagentica-plugin-provider",
		Name:         "TeamAgentica Plugin Provider",
		Version:      "1.0.0",
		Image:        "teamagentica-system-teamagentica-plugin-provider:latest",
		HTTPPort:     8083,
		Capabilities: []string{"marketplace:provider"},
	},
	{
		ID:           "system-user-manager",
		Name:         "User Manager",
		Version:      "0.1.0",
		Image:        "teamagentica-system-user-manager:latest",
		HTTPPort:     8090,
		Capabilities: []string{"system:users"},
	},
}

// seedSystemPlugins ensures all system plugins exist in the DB, are enabled,
// and have valid service tokens. Runs idempotently on every boot.
func seedSystemPlugins(db *gorm.DB) {
	for _, sp := range systemPlugins {
		var existing models.Plugin
		if db.First(&existing, "id = ?", sp.ID).Error == nil {
			// Already exists — ensure it's enabled and marked as system.
			updates := map[string]interface{}{
				"enabled": true,
				"system":  true,
				"name":    sp.Name,
			}
			if existing.Image != sp.Image {
				updates["image"] = sp.Image
			}
			db.Model(&existing).Updates(updates)

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

// loadCachedJWTSecret loads a cached copy of the JWT secret from the configs
// table so the kernel can validate service tokens at boot. The authoritative
// secret lives in the user-manager plugin; the kernel refreshes its cache
// after the plugin starts (see fetchJWTSecretFromPlugin in jwt_bootstrap.go).
func loadCachedJWTSecret(db *gorm.DB) {
	var row models.Config
	if db.Where("owner_id = ? AND key = ?", "kernel", "jwt_secret").First(&row).Error == nil {
		auth.InitJWT(row.Value)
		log.Println("database: JWT secret loaded from cache (will refresh from user-manager)")
		return
	}
	log.Println("database: no cached JWT secret — auth will be unavailable until user-manager starts")
}


