package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("008_requested_scopes", func(db *gorm.DB) error {
		return db.AutoMigrate(&models.Plugin{})
	})
}
