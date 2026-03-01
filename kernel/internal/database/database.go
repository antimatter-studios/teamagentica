package database

import (
	"log"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"roboslop/kernel/internal/models"
)

var DB *gorm.DB

func Init(dbPath string) {
	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := DB.AutoMigrate(&models.User{}, &models.Plugin{}, &models.PluginConfig{}, &models.ServiceToken{}, &models.AuditLog{}); err != nil {
		log.Fatalf("failed to auto-migrate: %v", err)
	}

	log.Println("database initialized at", dbPath)
}
