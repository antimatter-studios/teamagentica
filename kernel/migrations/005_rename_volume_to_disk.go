package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("005_rename_volume_to_disk", func(db *gorm.DB) error {
		// Rename volume_name → disk_name in managed_containers table.
		if db.Migrator().HasColumn(&models.ManagedContainer{}, "volume_name") {
			if err := db.Migrator().RenameColumn(&models.ManagedContainer{}, "volume_name", "disk_name"); err != nil {
				return err
			}
		}
		return db.AutoMigrate(&models.ManagedContainer{})
	})
}
