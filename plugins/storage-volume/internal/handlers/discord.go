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

// DiscordCommandVolumeList handles POST /discord-command/volume/list.
func (h *Handler) DiscordCommandVolumeList(c *gin.Context) {
	entries, err := os.ReadDir(h.volumesPath)
	if err != nil {
		discordText(c, "Failed to read volumes directory.")
		return
	}

	// Collect visible directories.
	var fields []pluginsdk.DiscordEmbedFieldResponse
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		size := dirSize(h.volumeDataPath(e.Name()))
		fields = append(fields, pluginsdk.DiscordEmbedFieldResponse{
			Name:   e.Name(),
			Value:  formatSize(size),
			Inline: true,
		})
	}

	if len(fields) == 0 {
		discordText(c, "No volumes found.")
		return
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type: "embed",
		Embeds: []pluginsdk.DiscordEmbedResponse{{
			Title:  fmt.Sprintf("Volumes (%d)", len(fields)),
			Color:  0x5865F2,
			Fields: fields,
		}},
	})
}

// DiscordCommandVolumeCreate handles POST /discord-command/volume/create.
func (h *Handler) DiscordCommandVolumeCreate(c *gin.Context) {
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
	if err := validateVolumeName(name); err != nil {
		discordText(c, err.Error())
		return
	}

	volType := req.Type
	if volType == "" {
		volType = "storage"
	}
	if err := validateVolumeType(volType); err != nil {
		discordText(c, err.Error())
		return
	}

	dataDir := h.volumeDataPath(name)
	if _, err := os.Stat(dataDir); err == nil {
		discordText(c, fmt.Sprintf("Volume %q already exists.", name))
		return
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[volumes] discord create dir error: %v", err)
		discordText(c, "Failed to create volume directory.")
		return
	}

	// Save metadata so it shows up with type info.
	vol := &Volume{
		Name:      name,
		Type:      volType,
		Labels:    map[string]string{},
		CreatedAt: time.Now().UTC(),
	}
	if err := h.saveVolumeMeta(vol); err != nil {
		log.Printf("[volumes] discord save meta error: %v", err)
		// Volume dir was created, that's fine — it'll show as unmanaged.
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type:    "text",
		Content: fmt.Sprintf("Created volume **%s** (type: %s)", name, volType),
	})
}

// DiscordCommandVolumeRename handles POST /discord-command/volume/rename.
func (h *Handler) DiscordCommandVolumeRename(c *gin.Context) {
	var req struct {
		Volume string `json:"volume"`
		Name   string `json:"name"`
	}
	c.ShouldBindJSON(&req)

	oldName := strings.TrimSpace(req.Volume)
	newName := strings.TrimSpace(req.Name)
	if oldName == "" || newName == "" {
		discordText(c, "Both volume and name are required.")
		return
	}
	if err := validateVolumeName(newName); err != nil {
		discordText(c, err.Error())
		return
	}

	oldPath := h.volumeDataPath(oldName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		discordText(c, fmt.Sprintf("Volume %q not found.", oldName))
		return
	}

	newPath := h.volumeDataPath(newName)
	if _, err := os.Stat(newPath); err == nil {
		discordText(c, fmt.Sprintf("Volume %q already exists.", newName))
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		log.Printf("[volumes] discord rename error: %v", err)
		discordText(c, "Failed to rename volume: "+err.Error())
		return
	}

	// Rename metadata file if it exists.
	oldMeta := h.metaPath(oldName)
	if _, err := os.Stat(oldMeta); err == nil {
		if vol, err := h.loadVolumeMeta(oldName); err == nil {
			vol.Name = newName
			h.saveVolumeMeta(vol)
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
