package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("004_price_subscription", func(db *gorm.DB) error {
		// Adds subscription column to model_prices.
		return db.AutoMigrate(&models.ModelPrice{})
	})
}
