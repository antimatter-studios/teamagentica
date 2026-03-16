package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("002_rename_configs", func(db *gorm.DB) error {
		// Check whether the old plugin_configs table still exists.
		var count int64
		db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='plugin_configs'").Scan(&count)
		if count == 0 {
			// Fresh install — 001 already created configs via the renamed model.
			return nil
		}

		// Create the new configs table.
		if err := db.AutoMigrate(&models.Config{}); err != nil {
			return err
		}

		// Copy rows, mapping plugin_id → owner_id.
		if err := db.Exec(`
			INSERT OR IGNORE INTO configs (id, owner_id, key, value, is_secret, created_at, updated_at)
			SELECT id, plugin_id, key, value, is_secret, created_at, updated_at FROM plugin_configs
		`).Error; err != nil {
			return err
		}

		// Drop the old table.
		return db.Exec("DROP TABLE plugin_configs").Error
	})
}
