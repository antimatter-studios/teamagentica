package migrations

import (
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/migrate"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// 009_multi_container adds Containers / ContainerIDs / DevMode columns to the
// plugin table to support multi-container plugins (a "pod" of containers).
// Existing single-container plugins continue to work unchanged because the
// kernel synthesizes a default container spec from the legacy top-level
// Image / DockerLabels / ExtraPorts fields when Containers is empty.
func init() {
	migrate.Register("009_multi_container", func(db *gorm.DB) error {
		return db.AutoMigrate(&models.Plugin{})
	})
}
