package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("003_candidate_slots", func(db *gorm.DB) error {
		// Adds candidate_image, candidate_version columns to plugins.
		return db.AutoMigrate(&models.Plugin{})
	})
}
