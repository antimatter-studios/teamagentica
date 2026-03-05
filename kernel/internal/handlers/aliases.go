package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ListAliases handles GET /api/aliases — returns all aliases.
// Available to both plugin-token and admin auth.
// Each alias includes the target plugin's capabilities for routing decisions.
func (h *PluginHandler) ListAliases(c *gin.Context) {
	infos := h.resolveAliases()
	c.JSON(http.StatusOK, gin.H{"aliases": infos})
}

// UpsertAlias handles POST /api/aliases — creates or updates a single alias.
func (h *PluginHandler) UpsertAlias(c *gin.Context) {
	var req models.Alias
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" || req.Target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and target are required"})
		return
	}

	// Upsert: create if not exists, update target if exists.
	var existing models.Alias
	if h.db.First(&existing, "name = ?", req.Name).Error != nil {
		if err := h.db.Create(&req).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create alias"})
			return
		}
	} else {
		h.db.Model(&existing).Update("target", req.Target)
	}

	h.Events.Emit(events.DebugEvent{
		Type:   "alias:upsert",
		Detail: fmt.Sprintf("name=%s target=%s", req.Name, req.Target),
	})

	h.broadcastAliasUpdate()
	c.JSON(http.StatusOK, gin.H{"alias": req})
}

// BulkReplaceAliases handles PUT /api/aliases — replaces admin-owned aliases.
// Plugin-owned aliases (plugin_id != '') are preserved.
func (h *PluginHandler) BulkReplaceAliases(c *gin.Context) {
	var req struct {
		Aliases []models.Alias `json:"aliases" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Only replace admin-owned aliases (plugin_id = ''), preserve plugin-owned ones.
	tx := h.db.Begin()
	if err := tx.Where("plugin_id = ''").Delete(&models.Alias{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear aliases"})
		return
	}
	for _, a := range req.Aliases {
		if a.Name == "" || a.Target == "" {
			continue
		}
		a.PluginID = "" // admin-created aliases have no plugin owner
		if err := tx.Create(&a).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to insert alias " + a.Name})
			return
		}
	}
	tx.Commit()

	h.Events.Emit(events.DebugEvent{
		Type:   "alias:bulk_replace",
		Detail: fmt.Sprintf("count=%d", len(req.Aliases)),
	})

	h.broadcastAliasUpdate()
	c.JSON(http.StatusOK, gin.H{"aliases": req.Aliases})
}

// DeleteAlias handles DELETE /api/aliases/:name.
func (h *PluginHandler) DeleteAlias(c *gin.Context) {
	name := c.Param("name")
	if result := h.db.Where("name = ?", name).Delete(&models.Alias{}); result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "alias not found"})
		return
	}

	h.Events.Emit(events.DebugEvent{
		Type:   "alias:delete",
		Detail: fmt.Sprintf("name=%s", name),
	})

	h.broadcastAliasUpdate()
	c.JSON(http.StatusOK, gin.H{"message": "alias deleted"})
}

// aliasInfo is a structured alias descriptor broadcast to subscribers.
type aliasInfo struct {
	Name         string   `json:"name"`
	Target       string   `json:"target"`
	PluginID     string   `json:"plugin_id"`
	Capabilities []string `json:"capabilities"`
}

// resolveAliases loads all aliases and enriches each with the target plugin's capabilities.
func (h *PluginHandler) resolveAliases() []aliasInfo {
	var aliases []models.Alias
	h.db.Order("name ASC").Find(&aliases)

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
	return infos
}

// broadcastAliasUpdate loads all aliases and broadcasts a kernel:alias:update event
// to all subscribers with the full alias list and a monotonic sequence number.
func (h *PluginHandler) broadcastAliasUpdate() {
	infos := h.resolveAliases()

	seq := h.broadcastSeq.Add(1)
	detail, _ := json.Marshal(map[string]interface{}{"aliases": infos})

	subs := h.Subs.GetSubscribers("kernel:alias:update")
	if len(subs) == 0 {
		return
	}

	payload := map[string]interface{}{
		"event_type": "kernel:alias:update",
		"plugin_id":  "kernel",
		"detail":     string(detail),
		"timestamp":  time.Now().Format(time.RFC3339),
		"seq":        seq,
	}
	body, _ := json.Marshal(payload)

	for _, sub := range subs {
		h.Events.Emit(events.DebugEvent{
			Type:     "dispatch",
			PluginID: sub.PluginID,
			Detail:   fmt.Sprintf("event=kernel:alias:update seq=%d callback=%s aliases=%d", seq, sub.CallbackPath, len(infos)),
		})
		go h.dispatchEvent(sub, body)
	}

	log.Printf("kernel:alias:update seq=%d broadcast to %d subscribers (%d aliases)", seq, len(subs), len(infos))
}

// debouncedAliasUpdate resets a 2-second timer. When the timer fires (no more
// registrations within 2s), it calls broadcastAliasUpdate with the full resolved list.
// This prevents N plugin registrations from triggering N broadcasts during boot.
func (h *PluginHandler) debouncedAliasUpdate() {
	h.aliasDebounceMu.Lock()
	defer h.aliasDebounceMu.Unlock()

	if h.aliasDebounceTimer != nil {
		h.aliasDebounceTimer.Stop()
	}
	h.aliasDebounceTimer = time.AfterFunc(2*time.Second, func() {
		h.broadcastAliasUpdate()
	})
}
