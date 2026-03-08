package storage

import (
	"log"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	db *gorm.DB
}

func Open(dataPath string) (*DB, error) {
	dbPath := filepath.Join(dataPath, "chat.db")
	conn, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	if err := conn.AutoMigrate(&Conversation{}, &Message{}); err != nil {
		return nil, err
	}
	log.Printf("[storage] database opened at %s", dbPath)
	return &DB{db: conn}, nil
}
