package watchdog

import (
	"errors"
	"log"
	"strings"
	"sync"

	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// DatabaseOps provides the database operations needed by the corruption watchdog.
type DatabaseOps struct {
	GetDB             func() *gorm.DB
	Reinit            func() error
	RestoreFromBackup func() error
}

var dbWatchdog = &databaseWatchdog{}

// databaseWatchdog detects SQLite corruption and closed-connection errors,
// auto-repairs via REINDEX, reconnect, or restore from backup.
type databaseWatchdog struct {
	mu        sync.Mutex
	repairing bool
	ops       DatabaseOps
}

// InitDatabase configures the database watchdog with the necessary operations.
func InitDatabase(ops DatabaseOps) {
	dbWatchdog.ops = ops
}

// CheckDatabaseError inspects a GORM error for SQLite corruption or a closed
// database connection. Only call this in error branches — it's a no-op for
// nil or unrecognised errors.
func CheckDatabaseError(err error) {
	if err == nil {
		return
	}

	if isClosedErr(err) {
		dbWatchdog.tryReconnect()
		return
	}

	code := corruptionCode(err)
	if code != 0 {
		dbWatchdog.tryRepair(code)
	}
}

// isClosedErr returns true if the error indicates the connection pool has been closed.
func isClosedErr(err error) bool {
	return strings.Contains(err.Error(), "database is closed")
}

// corruptionCode returns the SQLite error code for corruption (11) or not-a-db (26).
func corruptionCode(err error) sqlite3.ErrNo {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code {
		case sqlite3.ErrCorrupt, sqlite3.ErrNotADB:
			return sqliteErr.Code
		}
	}
	return 0
}

func (w *databaseWatchdog) tryRepair(code sqlite3.ErrNo) {
	w.mu.Lock()
	if w.repairing {
		w.mu.Unlock()
		return
	}
	w.repairing = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.repairing = false
		w.mu.Unlock()
	}()

	switch code {
	case sqlite3.ErrCorrupt:
		w.repairCorrupt()
	case sqlite3.ErrNotADB:
		w.restoreFromBackup()
	}
}

func (w *databaseWatchdog) tryReconnect() {
	w.mu.Lock()
	if w.repairing {
		w.mu.Unlock()
		return
	}
	w.repairing = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.repairing = false
		w.mu.Unlock()
	}()

	log.Println("watchdog/database: connection closed, attempting reconnect...")
	if err := w.ops.Reinit(); err != nil {
		log.Printf("watchdog/database: reconnect failed: %v", err)
		return
	}
	log.Println("watchdog/database: reconnected successfully")
}

func (w *databaseWatchdog) repairCorrupt() {
	log.Println("watchdog/database: SQLITE_CORRUPT detected, running REINDEX...")

	db := w.ops.GetDB()
	if err := db.Exec("REINDEX").Error; err != nil {
		log.Printf("watchdog/database: REINDEX failed: %v", err)
		w.restoreFromBackup()
		return
	}

	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		log.Printf("watchdog/database: integrity_check failed after REINDEX: %v", err)
		w.restoreFromBackup()
		return
	}

	if result == "ok" {
		log.Println("watchdog/database: REINDEX successful, integrity restored")
	} else {
		log.Printf("watchdog/database: REINDEX completed but integrity_check reports: %s", result)
		w.restoreFromBackup()
	}
}

func (w *databaseWatchdog) restoreFromBackup() {
	log.Println("watchdog/database: attempting restore from backup...")

	if err := w.ops.RestoreFromBackup(); err != nil {
		log.Printf("watchdog/database: restore failed: %v", err)
		log.Println("watchdog/database: requires manual recovery")
		return
	}

	log.Println("watchdog/database: restored from backup successfully")
}
