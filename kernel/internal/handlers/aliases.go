package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ListAliases handles GET /api/aliases — returns all aliases.
// Available to both plugin-token and admin auth.
// Each alias includes the target plugin's capabilities for routing decisions.
func (h *PluginHandler) ListAliases(c *gin.Context) {
	var aliases []models.Alias
	h.db.Order("name ASC").Find(&aliases)

	type aliasInfo struct {
		Name         string   `json:"name"`
		Target       string   `json:"target"`
		PluginID     string   `json:"plugin_id"`
		Capabilities []string `json:"capabilities"`
	}

	infos := make([]aliasInfo, 0, len(aliases))
	for _, a := range aliases {
		var caps []string
		var plugin models.Plugin
		// Target may include model suffix (e.g. "tool-nanobanana:gpt-4o"), strip it for lookup.
		pluginID := a.Target
		if idx := strings.Index(pluginID, ":"); idx > 0 {
			pluginID = pluginID[:idx]
		}
		if h.db.First(&plugin, "id = ?", pluginID).Error == nil {
			caps = plugin.GetCapabilities()
		}
		infos = append(infos, aliasInfo{
			Name:         a.Name,
			Target:       a.Target,
			PluginID:     a.PluginID,
			Capabilities: caps,
		})
	}
	c.JSON(http.StatusOK, gin.H{"aliases": infos})
}
