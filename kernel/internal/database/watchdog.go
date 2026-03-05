package database

import (
	"errors"
	"log"
	"sync"

	sqlite3 "github.com/mattn/go-sqlite3"
)

var watchdog = &corruptionWatchdog{}

// corruptionWatchdog detects SQLite corruption errors and auto-repairs via REINDEX
// or restores from backup for fatal corruption.
type corruptionWatchdog struct {
	mu        sync.Mutex
	repairing bool
}

// CheckError inspects a GORM error for SQLite corruption. Only call this in
// error branches — it's a no-op for nil or non-corruption errors.
func CheckError(err error) {
	if err == nil {
		return
	}
	code := corruptionCode(err)
	if code != 0 {
		watchdog.tryRepair(code)
	}
}

// corruptionCode returns the SQLite error code if the error represents
// corruption (SQLITE_CORRUPT=11) or a destroyed DB (SQLITE_NOTADB=26).
// Returns 0 for non-corruption errors.
func corruptionCode(err error) sqlite3.ErrNo {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code {
		case sqlite3.ErrCorrupt: // 11
			return sqliteErr.Code
		case sqlite3.ErrNotADB: // 26
			return sqliteErr.Code
		}
	}
	return 0
}

func (w *corruptionWatchdog) tryRepair(code sqlite3.ErrNo) {
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

// repairCorrupt handles SQLITE_CORRUPT (code 11) via REINDEX.
func (w *corruptionWatchdog) repairCorrupt() {
	log.Println("watchdog: SQLITE_CORRUPT detected, running REINDEX...")

	if err := DB.Exec("REINDEX").Error; err != nil {
		log.Printf("watchdog: REINDEX failed: %v", err)
		// REINDEX failed — fall back to restore.
		w.restoreFromBackup()
		return
	}

	var result string
	if err := DB.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		log.Printf("watchdog: integrity_check failed after REINDEX: %v", err)
		w.restoreFromBackup()
		return
	}

	if result == "ok" {
		log.Println("watchdog: REINDEX successful, database integrity restored")
	} else {
		log.Printf("watchdog: REINDEX completed but integrity_check reports: %s", result)
		log.Println("watchdog: attempting restore from backup...")
		w.restoreFromBackup()
	}
}

// restoreFromBackup handles SQLITE_NOTADB (code 26) or failed REINDEX
// by restoring from the most recent valid backup.
func (w *corruptionWatchdog) restoreFromBackup() {
	log.Println("watchdog: attempting database restore from backup...")

	if err := RestoreFromBackup(); err != nil {
		log.Printf("watchdog: restore failed: %v", err)
		log.Println("watchdog: database requires manual recovery")
		return
	}

	log.Println("watchdog: database restored from backup successfully")
}
