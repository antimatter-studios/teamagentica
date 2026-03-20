package pluginsdk

import (
	"log"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenDatabase creates or opens a SQLite database at dataPath/filename with the
// platform-standard DSN pragmas (WAL, 5s busy timeout, NORMAL sync,
// foreign keys ON). It runs AutoMigrate on all provided models.
//
// Usage:
//
//	db, err := pluginsdk.OpenDatabase("/data", "mydb.db", &User{}, &Token{})
func OpenDatabase(dataPath, filename string, models ...interface{}) (*gorm.DB, error) {
	dbPath := filepath.Join(dataPath, filename)
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			return nil, err
		}
	}

	log.Printf("[pluginsdk] database opened at %s", dbPath)
	return db, nil
}
