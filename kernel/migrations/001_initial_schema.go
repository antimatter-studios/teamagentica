package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func init() {
	migrate.Register("001_initial_schema", func(db *gorm.DB) error {
		return db.AutoMigrate(
			&models.User{},
			&models.Plugin{},
			&models.Config{},
			&models.ServiceToken{},
			&models.AuditLog{},
			&models.EventSubscription{},
			&models.ManagedContainer{},
		)
	})
}
