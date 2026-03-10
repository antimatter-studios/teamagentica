package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("008_managed_container_cmd", func(db *gorm.DB) error {
		if err := db.AutoMigrate(&models.ManagedContainer{}); err != nil {
			return err
		}
		return db.AutoMigrate(&models.Plugin{})
	})
}
