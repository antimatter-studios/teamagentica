package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("004_alias_plugin_id", func(db *gorm.DB) error {
		return db.AutoMigrate(&models.Alias{})
	})
}
