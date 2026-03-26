package storage

import (
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

type DB struct {
	db *gorm.DB
}

// DB returns the underlying GORM connection for custom queries.
func (d *DB) DB() *gorm.DB {
	return d.db
}

func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "chat.db", &Conversation{}, &Message{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}
