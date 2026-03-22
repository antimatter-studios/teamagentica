package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ListAliases handles GET /api/aliases — returns all aliases.
// Proxies to the infra-alias-registry plugin (source of truth) and enriches
// each alias with the target plugin's capabilities for routing decisions.
func (h *PluginHandler) ListAliases(c *gin.Context) {
	// Look up the alias-registry plugin.
	var registry models.Plugin
	if err := h.db.First(&registry, "id = ?", "infra-alias-registry").Error; err != nil {
		log.Printf("aliases: infra-alias-registry not found, returning empty list: %v", err)
		c.JSON(http.StatusOK, gin.H{"aliases": []interface{}{}})
		return
	}

	// Fetch aliases from the registry plugin.
	url := fmt.Sprintf("%s://%s:%d/aliases", h.pluginScheme(), registry.Host, registry.HTTPPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}

	resp, err := h.proxyClient().Do(req)
	if err != nil {
		log.Printf("aliases: failed to fetch from alias-registry: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "alias-registry unreachable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("aliases: alias-registry returned status %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "alias-registry error"})
		return
	}

	// Parse the registry response.
	var registryResp struct {
		Aliases []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Plugin   string `json:"plugin"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"aliases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registryResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode alias-registry response"})
		return
	}

	// Transform into the API format expected by SDK consumers.
	type aliasInfo struct {
		Name         string   `json:"name"`
		Target       string   `json:"target"`
		PluginID     string   `json:"plugin_id"`
		Capabilities []string `json:"capabilities"`
	}

	infos := make([]aliasInfo, 0, len(registryResp.Aliases))
	for _, a := range registryResp.Aliases {
		// Build target: "plugin:model" if model is set, otherwise just "plugin".
		target := a.Plugin
		if a.Model != "" {
			target = a.Plugin + ":" + a.Model
		}

		// Look up plugin capabilities.
		pluginID := a.Plugin
		var caps []string
		var plugin models.Plugin
		if h.db.First(&plugin, "id = ?", pluginID).Error == nil {
			caps = plugin.GetCapabilities()
		}

		infos = append(infos, aliasInfo{
			Name:         a.Name,
			Target:       target,
			PluginID:     a.Plugin,
			Capabilities: caps,
		})
	}

	c.JSON(http.StatusOK, gin.H{"aliases": infos})
}
