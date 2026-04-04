package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// ChatCommandDiskList handles POST /chat-command/disk/list.
func (h *Handler) ChatCommandDiskList(c *gin.Context) {
	var fields []pluginsdk.EmbedField
	for _, diskType := range []string{"shared", "workspace"} {
		entries, err := os.ReadDir(h.typePath(diskType))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			size := dirSize(h.diskDataPath(diskType, e.Name()))
			fields = append(fields, pluginsdk.EmbedField{
				Name:   fmt.Sprintf("[%s] %s", diskType, e.Name()),
				Value:  formatSize(size),
				Inline: true,
			})
		}
	}

	if len(fields) == 0 {
		chatText(c, "No disks found.")
		return
	}

	c.JSON(http.StatusOK, pluginsdk.EmbedResponse(pluginsdk.EmbedContent{
		Title:  fmt.Sprintf("Disks (%d)", len(fields)),
		Color:  0x5865F2,
		Fields: fields,
	}))
}

// ChatCommandDiskCreate handles POST /chat-command/disk/create.
func (h *Handler) ChatCommandDiskCreate(c *gin.Context) {
	var req struct {
		Params map[string]string `json:"params"`
	}
	c.ShouldBindJSON(&req)

	name := strings.TrimSpace(req.Params["name"])
	if name == "" {
		chatError(c, "Name is required.")
		return
	}
	if err := validateDiskName(name); err != nil {
		chatError(c, err.Error())
		return
	}

	diskType := req.Params["type"]
	if diskType == "" {
		diskType = "workspace"
	}
	if err := validateDiskType(diskType); err != nil {
		chatError(c, err.Error())
		return
	}

	dataDir := h.diskDataPath(diskType, name)
	if _, err := os.Stat(dataDir); err == nil {
		chatError(c, fmt.Sprintf("Disk %q already exists.", name))
		return
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[disks] chat-cmd create dir error: %v", err)
		chatError(c, "Failed to create disk directory.")
		return
	}

	d := &Disk{
		Name:      name,
		Type:      diskType,
		Labels:    map[string]string{},
		CreatedAt: time.Now().UTC(),
	}
	if err := h.saveDiskMeta(d); err != nil {
		log.Printf("[disks] chat-cmd save meta error: %v", err)
	}

	chatText(c, fmt.Sprintf("Created disk %s (type: %s)", name, diskType))
}

// ChatCommandDiskRename handles POST /chat-command/disk/rename.
func (h *Handler) ChatCommandDiskRename(c *gin.Context) {
	var req struct {
		Params map[string]string `json:"params"`
	}
	c.ShouldBindJSON(&req)

	oldName := strings.TrimSpace(req.Params["disk"])
	newName := strings.TrimSpace(req.Params["name"])
	if oldName == "" || newName == "" {
		chatError(c, "Both disk and name are required.")
		return
	}
	if err := validateDiskName(newName); err != nil {
		chatError(c, err.Error())
		return
	}

	oldType := h.findDiskType(oldName)
	oldPath := h.diskDataPath(oldType, oldName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		chatError(c, fmt.Sprintf("Disk %q not found.", oldName))
		return
	}

	newPath := h.diskDataPath(oldType, newName)
	if _, err := os.Stat(newPath); err == nil {
		chatError(c, fmt.Sprintf("Disk %q already exists.", newName))
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		log.Printf("[disks] chat-cmd rename error: %v", err)
		chatError(c, "Failed to rename disk: "+err.Error())
		return
	}

	// Update metadata in DB.
	if d, err := h.loadDiskMeta(oldName); err == nil {
		h.deleteDiskMeta(oldName)
		d.Name = newName
		h.saveDiskMeta(d)
	}

	chatText(c, fmt.Sprintf("Renamed %s → %s", oldName, newName))
}

func chatText(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.TextResponse(msg))
}

func chatError(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.ErrorResponse(msg))
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
