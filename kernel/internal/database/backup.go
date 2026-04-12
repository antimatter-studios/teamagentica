package database

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// BackupManager performs two tiers of online backups using SQLite's Backup API:
//
//  1. Rotating snapshots: frequent (default 5m), 3 slots — for crash/corruption recovery.
//  2. Daily archives: one per day, kept for 30 days — for real data-loss recovery.
type BackupManager struct {
	dbPath    string
	backupDir string

	// Tier 1 — rotating snapshots.
	slots       int
	interval    time.Duration
	currentSlot int

	// Tier 2 — daily archives.
	archiveDir   string
	archiveDays  int
	lastArchive  string // "2006-01-02" of last archive taken
}

var backupMgr *BackupManager

// StartBackups initialises and starts the background backup manager.
func StartBackups(ctx context.Context, dataDir string, interval time.Duration) {
	dir := filepath.Join(dataDir, "backups")
	archiveDir := filepath.Join(dir, "daily")
	for _, d := range []string{dir, archiveDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Printf("backup: failed to create dir %s: %v", d, err)
			return
		}
	}

	backupMgr = &BackupManager{
		dbPath:      DBPath(),
		backupDir:   dir,
		slots:       3,
		interval:    interval,
		archiveDir:  archiveDir,
		archiveDays: 30,
	}

	go backupMgr.Start(ctx)
}

// Start runs the periodic backup loop.
func (bm *BackupManager) Start(ctx context.Context) {
	log.Printf("backup: started (snapshots: interval=%s slots=%d, archives: daily keep=%dd, dir=%s)",
		bm.interval, bm.slots, bm.archiveDays, bm.backupDir)

	// Take an initial snapshot + archive check immediately on startup.
	bm.performBackup()
	bm.checkDailyArchive()

	ticker := time.NewTicker(bm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("backup: stopped")
			return
		case <-ticker.C:
			bm.performBackup()
			bm.checkDailyArchive()
		}
	}
}

// performBackup takes a rotating snapshot.
func (bm *BackupManager) performBackup() {
	slot := bm.currentSlot % bm.slots
	destPath := filepath.Join(bm.backupDir, fmt.Sprintf("backup-%d.db", slot))

	if err := bm.doBackup(destPath); err != nil {
		log.Printf("backup: snapshot slot %d failed: %v", slot, err)
		return
	}

	bm.currentSlot++
}

// checkDailyArchive creates a daily archive if one hasn't been taken today,
// then prunes archives older than archiveDays.
func (bm *BackupManager) checkDailyArchive() {
	today := time.Now().UTC().Format("2006-01-02")
	if today == bm.lastArchive {
		return
	}

	destPath := filepath.Join(bm.archiveDir, fmt.Sprintf("archive-%s.db", today))
	if err := bm.doBackup(destPath); err != nil {
		log.Printf("backup: daily archive failed: %v", err)
		return
	}

	bm.lastArchive = today
	log.Printf("backup: daily archive created: %s", destPath)

	bm.pruneArchives()
}

// pruneArchives removes daily archives older than archiveDays.
func (bm *BackupManager) pruneArchives() {
	cutoff := time.Now().UTC().AddDate(0, 0, -bm.archiveDays).Format("2006-01-02")

	entries, err := os.ReadDir(bm.archiveDir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "archive-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		// Extract date: "archive-2026-04-01.db" → "2026-04-01"
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, "archive-"), ".db")
		if dateStr < cutoff {
			path := filepath.Join(bm.archiveDir, name)
			if err := os.Remove(path); err != nil {
				log.Printf("backup: failed to prune %s: %v", name, err)
			} else {
				log.Printf("backup: pruned old archive %s", name)
			}
		}
	}
}

// doBackup performs the actual SQLite backup API copy to destPath.
func (bm *BackupManager) doBackup(destPath string) error {
	start := time.Now()

	srcConn, err := openRawConn(bm.dbPath + "?mode=ro")
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcConn.Close()

	destConn, err := openRawConn(destPath)
	if err != nil {
		return fmt.Errorf("open dest: %w", err)
	}
	defer destConn.Close()

	backup, err := destConn.Backup("main", srcConn, "main")
	if err != nil {
		return fmt.Errorf("init backup: %w", err)
	}

	done, err := backup.Step(-1)
	if err != nil {
		backup.Finish()
		return fmt.Errorf("step: %w", err)
	}
	if !done {
		log.Printf("backup: step returned not-done for %s", destPath)
	}

	pageCount := backup.PageCount()
	if err := backup.Finish(); err != nil {
		return fmt.Errorf("finish: %w", err)
	}

	log.Printf("backup: %s completed (%dms, %d pages)", filepath.Base(destPath), time.Since(start).Milliseconds(), pageCount)
	return nil
}

// Restore finds the newest valid backup across both tiers and replaces the main database.
func (bm *BackupManager) Restore() error {
	backupPath := bm.newestValidBackup()
	if backupPath == "" {
		return fmt.Errorf("no valid backup found")
	}

	log.Printf("backup: restoring from %s", backupPath)

	sqlDB, err := DB.DB()
	if err == nil {
		sqlDB.Close()
	}

	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}

	if err := os.WriteFile(bm.dbPath, data, 0o644); err != nil {
		return fmt.Errorf("write db: %w", err)
	}

	os.Remove(bm.dbPath + "-wal")
	os.Remove(bm.dbPath + "-shm")

	if err := Reinit(); err != nil {
		return fmt.Errorf("reinit after restore: %w", err)
	}

	log.Println("backup: database restored successfully")
	return nil
}

// newestValidBackup checks rotating snapshots first (most recent), then
// daily archives newest-first. Returns the first file passing integrity check.
func (bm *BackupManager) newestValidBackup() string {
	// Check rotating snapshots (newest first).
	for i := 0; i < bm.slots; i++ {
		slot := ((bm.currentSlot - 1 - i) % bm.slots + bm.slots) % bm.slots
		path := filepath.Join(bm.backupDir, fmt.Sprintf("backup-%d.db", slot))
		if bm.isValidBackup(path) {
			return path
		}
	}

	// Check daily archives (newest first).
	entries, err := os.ReadDir(bm.archiveDir)
	if err != nil {
		return ""
	}

	// Sort descending by name (date-based names sort correctly).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})

	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "archive-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		path := filepath.Join(bm.archiveDir, e.Name())
		if bm.isValidBackup(path) {
			return path
		}
	}

	return ""
}

// isValidBackup checks if a backup file exists and passes SQLite integrity check.
func (bm *BackupManager) isValidBackup(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}

	conn, err := openRawConn(path)
	if err != nil {
		return false
	}
	defer conn.Close()

	var result string
	rows, err := conn.Query("PRAGMA integrity_check", nil)
	if err != nil {
		return false
	}
	dest := make([]driver.Value, 1)
	if rows.Next(dest) == nil {
		if s, ok := dest[0].(string); ok {
			result = s
		}
	}
	rows.Close()

	if result != "ok" {
		log.Printf("backup: %s failed integrity check: %s", filepath.Base(path), result)
		return false
	}
	return true
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
