package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

type Handler struct {
	dataPath    string
	volumesPath string
	debug       bool
}

func NewHandler(dataPath, volumesPath string, debug bool) *Handler {
	return &Handler{
		dataPath:    dataPath,
		volumesPath: volumesPath,
		debug:       debug,
	}
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
	stats, err := getDiskStats(h.volumesPath)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"plugin": "storage-volume",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "storage-volume",
		"version":    "1.1.0",
		"disk_usage": stats,
	})
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

// Tools returns the tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(c *gin.Context) {
	tools := VolumeToolDefs()
	tools = append(tools, StorageAPIToolDefs()...)
	c.JSON(http.StatusOK, gin.H{"tools": tools})
}
