package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("003_aliases", func(db *gorm.DB) error {
		if err := db.AutoMigrate(&models.Alias{}); err != nil {
			return err
		}
		// Add event_port column to plugins table.
		return db.AutoMigrate(&models.Plugin{})
	})
}
