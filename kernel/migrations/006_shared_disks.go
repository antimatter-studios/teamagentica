package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("006_shared_disks", func(db *gorm.DB) error {
		// Adds shared_disks column to plugins table.
		return db.AutoMigrate(&models.Plugin{})
	})
}
