package database

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// BackupManager performs rotating online backups using SQLite's Backup API.
type BackupManager struct {
	dbPath      string
	backupDir   string
	slots       int
	interval    time.Duration
	currentSlot int
}

var backupMgr *BackupManager

// StartBackups initialises and starts the background backup manager.
func StartBackups(ctx context.Context, dataDir string, interval time.Duration) {
	dir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("backup: failed to create backup dir: %v", err)
		return
	}

	backupMgr = &BackupManager{
		dbPath:    DBPath(),
		backupDir: dir,
		slots:     3,
		interval:  interval,
	}

	go backupMgr.Start(ctx)
}

// Start runs the periodic backup loop.
func (bm *BackupManager) Start(ctx context.Context) {
	log.Printf("backup: started (interval=%s, slots=%d, dir=%s)", bm.interval, bm.slots, bm.backupDir)

	// Take an initial backup immediately on startup.
	bm.performBackup()

	ticker := time.NewTicker(bm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("backup: stopped")
			return
		case <-ticker.C:
			bm.performBackup()
		}
	}
}

// performBackup takes a consistent snapshot using SQLite's Backup API.
// Opens a dedicated read-only source connection for each backup to avoid
// conflicting with GORM's connection pool (SQLite C API is not thread-safe
// for backup operations on a shared connection).
func (bm *BackupManager) performBackup() {
	slot := bm.currentSlot % bm.slots
	destPath := filepath.Join(bm.backupDir, fmt.Sprintf("backup-%d.db", slot))

	start := time.Now()

	// Open a dedicated read-only source connection.
	srcConn, err := openRawConn(bm.dbPath + "?mode=ro")
	if err != nil {
		log.Printf("backup: failed to open source: %v", err)
		return
	}
	defer srcConn.Close()

	// Open a destination connection.
	destConn, err := openRawConn(destPath)
	if err != nil {
		log.Printf("backup: failed to open dest %s: %v", destPath, err)
		return
	}
	defer destConn.Close()

	backup, err := destConn.Backup("main", srcConn, "main")
	if err != nil {
		log.Printf("backup: failed to init backup to slot %d: %v", slot, err)
		return
	}

	done, err := backup.Step(-1)
	if err != nil {
		log.Printf("backup: step failed for slot %d: %v", slot, err)
		backup.Finish()
		return
	}
	if !done {
		log.Printf("backup: step returned not-done for slot %d", slot)
	}

	pageCount := backup.PageCount()

	if err := backup.Finish(); err != nil {
		log.Printf("backup: finish failed for slot %d: %v", slot, err)
		return
	}

	elapsed := time.Since(start)
	log.Printf("backup: snapshot #%d completed (%dms, %d pages)", slot, elapsed.Milliseconds(), pageCount)

	bm.currentSlot++
}

// Restore finds the newest valid backup and replaces the main database file.
// Returns an error if no valid backup is available.
func (bm *BackupManager) Restore() error {
	backupPath := bm.newestValidBackup()
	if backupPath == "" {
		return fmt.Errorf("no valid backup found in %s", bm.backupDir)
	}

	log.Printf("backup: restoring from %s", backupPath)

	// Close the current DB connection before replacing the file.
	sqlDB, err := DB.DB()
	if err == nil {
		sqlDB.Close()
	}

	// Copy backup over the main DB file.
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("backup: read backup file: %w", err)
	}

	if err := os.WriteFile(bm.dbPath, data, 0o644); err != nil {
		return fmt.Errorf("backup: write db file: %w", err)
	}

	// Remove WAL/SHM files that belong to the old (corrupt) database.
	os.Remove(bm.dbPath + "-wal")
	os.Remove(bm.dbPath + "-shm")

	// Reinitialise the GORM connection.
	if err := Reinit(); err != nil {
		return fmt.Errorf("backup: reinit after restore: %w", err)
	}

	log.Println("backup: database restored successfully")
	return nil
}

// newestValidBackup scans slots newest-first and returns the first passing integrity check.
func (bm *BackupManager) newestValidBackup() string {
	for i := 0; i < bm.slots; i++ {
		slot := ((bm.currentSlot - 1 - i) % bm.slots + bm.slots) % bm.slots
		path := filepath.Join(bm.backupDir, fmt.Sprintf("backup-%d.db", slot))

		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}

		conn, err := openRawConn(path)
		if err != nil {
			continue
		}

		var result string
		rows, err := conn.Query("PRAGMA integrity_check", nil)
		if err == nil {
			dest := make([]driver.Value, 1)
			if rows.Next(dest) == nil {
				if s, ok := dest[0].(string); ok {
					result = s
				}
			}
			rows.Close()
		}
		conn.Close()

		if result == "ok" {
			return path
		}
		log.Printf("backup: slot %d failed integrity check: %s", slot, result)
	}

	return ""
}

// RestoreFromBackup is the package-level function called by the watchdog.
func RestoreFromBackup() error {
	if backupMgr == nil {
		return fmt.Errorf("backup manager not initialised")
	}
	return backupMgr.Restore()
}

// openRawConn opens a raw sqlite3 connection for backup operations.
func openRawConn(path string) (*sqlite3.SQLiteConn, error) {
	drv := &sqlite3.SQLiteDriver{}
	rawConn, err := drv.Open(path)
	if err != nil {
		return nil, err
	}
	conn, ok := rawConn.(*sqlite3.SQLiteConn)
	if !ok {
		return nil, fmt.Errorf("unexpected connection type")
	}
	return conn, nil
}
