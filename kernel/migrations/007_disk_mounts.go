package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("007_disk_mounts", func(db *gorm.DB) error {
		// Adds disk_mounts column to managed_containers table,
		// replacing the old disk_name and extra_mounts columns.
		return db.AutoMigrate(&models.ManagedContainer{})
	})
}
