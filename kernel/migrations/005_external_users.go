package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("005_external_users", func(db *gorm.DB) error {
		return db.AutoMigrate(&models.ExternalUser{})
	})
}
