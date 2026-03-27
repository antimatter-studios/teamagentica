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

// DiscordCommandDiskList handles POST /discord-command/disk/list.
func (h *Handler) DiscordCommandDiskList(c *gin.Context) {
	entries, err := os.ReadDir(h.disksPath)
	if err != nil {
		discordText(c, "Failed to read disks directory.")
		return
	}

	// Collect visible directories.
	var fields []pluginsdk.DiscordEmbedFieldResponse
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		size := dirSize(h.diskDataPath(e.Name()))
		fields = append(fields, pluginsdk.DiscordEmbedFieldResponse{
			Name:   e.Name(),
			Value:  formatSize(size),
			Inline: true,
		})
	}

	if len(fields) == 0 {
		discordText(c, "No disks found.")
		return
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type: "embed",
		Embeds: []pluginsdk.DiscordEmbedResponse{{
			Title:  fmt.Sprintf("Disks (%d)", len(fields)),
			Color:  0x5865F2,
			Fields: fields,
		}},
	})
}

// DiscordCommandDiskCreate handles POST /discord-command/disk/create.
func (h *Handler) DiscordCommandDiskCreate(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	c.ShouldBindJSON(&req)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		discordText(c, "Name is required.")
		return
	}
	if err := validateDiskName(name); err != nil {
		discordText(c, err.Error())
		return
	}

	diskType := req.Type
	if diskType == "" {
		diskType = "storage"
	}
	if err := validateDiskType(diskType); err != nil {
		discordText(c, err.Error())
		return
	}

	dataDir := h.diskDataPath(name)
	if _, err := os.Stat(dataDir); err == nil {
		discordText(c, fmt.Sprintf("Disk %q already exists.", name))
		return
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[disks] discord create dir error: %v", err)
		discordText(c, "Failed to create disk directory.")
		return
	}

	// Save metadata so it shows up with type info.
	d := &Disk{
		Name:      name,
		Type:      diskType,
		Labels:    map[string]string{},
		CreatedAt: time.Now().UTC(),
	}
	if err := h.saveDiskMeta(d); err != nil {
		log.Printf("[disks] discord save meta error: %v", err)
		// Disk dir was created, that's fine — it'll show as unmanaged.
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type:    "text",
		Content: fmt.Sprintf("Created disk **%s** (type: %s)", name, diskType),
	})
}

// DiscordCommandDiskRename handles POST /discord-command/disk/rename.
func (h *Handler) DiscordCommandDiskRename(c *gin.Context) {
	var req struct {
		Disk string `json:"disk"`
		Name string `json:"name"`
	}
	c.ShouldBindJSON(&req)

	oldName := strings.TrimSpace(req.Disk)
	newName := strings.TrimSpace(req.Name)
	if oldName == "" || newName == "" {
		discordText(c, "Both disk and name are required.")
		return
	}
	if err := validateDiskName(newName); err != nil {
		discordText(c, err.Error())
		return
	}

	oldPath := h.diskDataPath(oldName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		discordText(c, fmt.Sprintf("Disk %q not found.", oldName))
		return
	}

	newPath := h.diskDataPath(newName)
	if _, err := os.Stat(newPath); err == nil {
		discordText(c, fmt.Sprintf("Disk %q already exists.", newName))
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		log.Printf("[disks] discord rename error: %v", err)
		discordText(c, "Failed to rename disk: "+err.Error())
		return
	}

	// Rename metadata file if it exists.
	oldMeta := h.metaPath(oldName)
	if _, err := os.Stat(oldMeta); err == nil {
		if d, err := h.loadDiskMeta(oldName); err == nil {
			d.Name = newName
			h.saveDiskMeta(d)
			os.Remove(oldMeta)
		} else {
			os.Rename(oldMeta, h.metaPath(newName))
		}
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type:    "text",
		Content: fmt.Sprintf("Renamed **%s** → **%s**", oldName, newName),
	})
}

func discordText(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{Type: "text", Content: msg})
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
