package handlers

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

type Handler struct {
	dataPath    string
	storageRoot string
	debug       bool
	db          *gorm.DB
}

func NewHandler(dataPath, storageRoot string, debug bool) *Handler {
	dbDir := filepath.Join(dataPath, "database")
	os.MkdirAll(dbDir, 0755)
	db, err := pluginsdk.OpenDatabase(dbDir, "storage.db", &DiskRecord{})
	if err != nil {
		log.Fatalf("[storage-disk] failed to open database: %v", err)
	}

	backfillDiskIDs(db)

	return &Handler{
		dataPath:    dataPath,
		storageRoot: storageRoot,
		debug:       debug,
		db:          db,
	}
}

// DiskRecord stores disk metadata in the database.
type DiskRecord struct {
	ID        string `gorm:"uniqueIndex;size:36"` // stable UUID — survives renames
	Name      string `gorm:"primaryKey"`
	Type      string `gorm:"default:workspace"`
	Labels    string // JSON object
	CreatedAt time.Time
	UpdatedAt time.Time
}

// backfillDiskIDs assigns UUIDs to any DiskRecord rows that have an empty ID.
func backfillDiskIDs(db *gorm.DB) {
	var recs []DiskRecord
	db.Where("id = '' OR id IS NULL").Find(&recs)
	for _, r := range recs {
		r.ID = generateDiskID()
		db.Save(&r)
	}
	if len(recs) > 0 {
		log.Printf("[storage-disk] backfilled IDs for %d disk(s)", len(recs))
	}
}

func generateDiskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based (extremely unlikely).
		return fmt.Sprintf("disk-%d", time.Now().UnixNano())
	}
	// UUID v4 format.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type diskStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

func getDiskStats(path string) (*diskStats, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return &diskStats{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: free,
		UsedPercent:    pct,
	}, nil
}

func (h *Handler) Health(c *gin.Context) {
	stats, err := getDiskStats(h.storageRoot)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"plugin": "storage-disk",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "storage-disk",
		"version":    "1.1.0",
		"disk_usage": stats,
	})
}

// moveToTrash copies a file or directory to .trash preserving its relative path,
// then removes the original only after the copy succeeds.
// root is the type directory (e.g. storageRoot/shared) that contains .trash.
func (h *Handler) moveToTrash(root, fullPath string) error {
	rel, err := filepath.Rel(root, fullPath)
	if err != nil {
		return fmt.Errorf("compute relative path: %w", err)
	}

	// Never trash the .trash directory itself.
	if strings.HasPrefix(rel, ".trash") {
		return fmt.Errorf("cannot trash the .trash directory")
	}

	trashDest := filepath.Join(root, ".trash", rel)

	// Handle collision: if dest already exists, append a timestamp suffix.
	if _, err := os.Stat(trashDest); err == nil {
		ts := time.Now().UTC().Format("20060102T150405")
		ext := filepath.Ext(trashDest)
		base := strings.TrimSuffix(trashDest, ext)
		trashDest = base + "." + ts + ext
		// If still collides (sub-second), add a counter.
		for i := 2; ; i++ {
			if _, err := os.Stat(trashDest); os.IsNotExist(err) {
				break
			}
			trashDest = base + "." + ts + "." + strconv.Itoa(i) + ext
		}
	}

	if err := os.MkdirAll(filepath.Dir(trashDest), 0755); err != nil {
		return fmt.Errorf("create trash dir: %w", err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	// Copy to trash (copy, not move, so original stays until copy is verified).
	if info.IsDir() {
		if err := copyDirRecursive(fullPath, trashDest); err != nil {
			return fmt.Errorf("copy to trash: %w", err)
		}
	} else {
		if err := copySingleFile(fullPath, trashDest); err != nil {
			return fmt.Errorf("copy to trash: %w", err)
		}
	}

	// Verify the trash copy exists before removing original.
	if _, err := os.Stat(trashDest); err != nil {
		return fmt.Errorf("trash copy verification failed: %w", err)
	}

	// Remove original.
	if info.IsDir() {
		if err := os.RemoveAll(fullPath); err != nil {
			return fmt.Errorf("remove original after trash: %w", err)
		}
	} else {
		if err := os.Remove(fullPath); err != nil {
			return fmt.Errorf("remove original after trash: %w", err)
		}
	}

	log.Printf("[storage] trashed %s -> %s", rel, trashDest)
	return nil
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// ToolDefs returns the raw tool definitions for use in SDK schema registration.
func (h *Handler) ToolDefs() interface{} {
	tools := DiskToolDefs()
	tools = append(tools, StorageAPIToolDefs()...)
	tools = append(tools, TrashToolDefs()...)
	return tools
}

// Tools returns the tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}
