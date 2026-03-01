package handlers

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"roboslop/kernel/internal/config"
	"roboslop/kernel/internal/models"
	"roboslop/kernel/internal/runtime"
)

// PluginHandler holds dependencies for plugin management endpoints.
type PluginHandler struct {
	db        *gorm.DB
	runtime   *runtime.DockerRuntime
	cfg       *config.Config
	clientTLS *tls.Config
}

// NewPluginHandler creates a new PluginHandler.
// clientTLS is optional; pass nil to disable mTLS for proxied requests.
func NewPluginHandler(db *gorm.DB, rt *runtime.DockerRuntime, cfg *config.Config, clientTLS *tls.Config) *PluginHandler {
	return &PluginHandler{db: db, runtime: rt, cfg: cfg, clientTLS: clientTLS}
}

// --- request/response types ---

type registerPluginRequest struct {
	ID           string          `json:"id" binding:"required"`
	Name         string          `json:"name" binding:"required"`
	Version      string          `json:"version" binding:"required"`
	Image        string          `json:"image" binding:"required"`
	GRPCPort     int             `json:"grpc_port"`
	HTTPPort     int             `json:"http_port"`
	Capabilities []string        `json:"capabilities"`
	ConfigSchema json.RawMessage `json:"config_schema"`
}

type updateConfigRequest struct {
	Config map[string]configEntry `json:"config" binding:"required"`
}

type configEntry struct {
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// --- handlers ---

// RegisterPlugin handles POST /api/plugins.
func (h *PluginHandler) RegisterPlugin(c *gin.Context) {
	var req registerPluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check for duplicate.
	var existing models.Plugin
	if err := h.db.First(&existing, "id = ?", req.ID).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plugin already registered"})
		return
	}

	plugin := models.Plugin{
		ID:       req.ID,
		Name:     req.Name,
		Version:  req.Version,
		Image:    req.Image,
		GRPCPort: req.GRPCPort,
		HTTPPort: req.HTTPPort,
	}
	plugin.SetCapabilities(req.Capabilities)

	if req.ConfigSchema != nil {
		plugin.ConfigSchema = string(req.ConfigSchema)
	}

	if result := h.db.Create(&plugin); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register plugin"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"plugin": plugin})
}

// ListPlugins handles GET /api/plugins.
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	var plugins []models.Plugin
	if result := h.db.Find(&plugins); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch plugins"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plugins": plugins})
}

// GetPlugin handles GET /api/plugins/:id.
func (h *PluginHandler) GetPlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plugin": plugin})
}

// UninstallPlugin handles DELETE /api/plugins/:id.
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	// Stop container if running.
	if plugin.ContainerID != "" && h.runtime != nil {
		_ = h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID)
	}

	// Remove config entries.
	h.db.Where("plugin_id = ?", id).Delete(&models.PluginConfig{})

	// Remove plugin record (data volume is kept).
	if result := h.db.Delete(&plugin); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete plugin"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "plugin uninstalled"})
}

// EnablePlugin handles POST /api/plugins/:id/enable.
func (h *PluginHandler) EnablePlugin(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	// Build env from config table.
	env := h.buildEnv(id)

	// Inject kernel host/port.
	env["ROBOSLOP_KERNEL_HOST"] = h.cfg.Host
	env["ROBOSLOP_KERNEL_PORT"] = h.cfg.Port

	// Pull image first.
	if err := h.runtime.PullImage(c.Request.Context(), plugin.Image); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to pull image: " + err.Error()})
		return
	}

	containerID, err := h.runtime.StartPlugin(c.Request.Context(), &plugin, env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start plugin: " + err.Error()})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"status":       "running",
		"enabled":      true,
	})

	c.JSON(http.StatusOK, gin.H{"message": "plugin enabled", "container_id": containerID})
}

// DisablePlugin handles POST /api/plugins/:id/disable.
func (h *PluginHandler) DisablePlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.ContainerID != "" && h.runtime != nil {
		if err := h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop plugin: " + err.Error()})
			return
		}
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"container_id": "",
		"status":       "stopped",
		"enabled":      false,
	})

	c.JSON(http.StatusOK, gin.H{"message": "plugin disabled"})
}

// RestartPlugin handles POST /api/plugins/:id/restart.
func (h *PluginHandler) RestartPlugin(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	// Stop existing container if running.
	if plugin.ContainerID != "" {
		_ = h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID)
	}

	// Re-start with current config.
	env := h.buildEnv(id)
	env["ROBOSLOP_KERNEL_HOST"] = h.cfg.Host
	env["ROBOSLOP_KERNEL_PORT"] = h.cfg.Port

	containerID, err := h.runtime.StartPlugin(c.Request.Context(), &plugin, env)
	if err != nil {
		h.db.Model(&plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "error",
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restart plugin: " + err.Error()})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"status":       "running",
	})

	c.JSON(http.StatusOK, gin.H{"message": "plugin restarted", "container_id": containerID})
}

// GetPluginLogs handles GET /api/plugins/:id/logs.
func (h *PluginHandler) GetPluginLogs(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.ContainerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plugin has no running container"})
		return
	}

	tail := 100
	if t := c.Query("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 {
			tail = v
		}
	}

	logs, err := h.runtime.ContainerLogs(c.Request.Context(), plugin.ContainerID, tail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get logs: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// GetPluginConfig handles GET /api/plugins/:id/config.
func (h *PluginHandler) GetPluginConfig(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	var configs []models.PluginConfig
	h.db.Where("plugin_id = ?", id).Find(&configs)

	// Mask secrets.
	type configItem struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	items := make([]configItem, len(configs))
	for i, cfg := range configs {
		val := cfg.Value
		if cfg.IsSecret {
			val = "********"
		}
		items[i] = configItem{Key: cfg.Key, Value: val, IsSecret: cfg.IsSecret}
	}

	c.JSON(http.StatusOK, gin.H{"config": items})
}

// UpdatePluginConfig handles PUT /api/plugins/:id/config.
func (h *PluginHandler) UpdatePluginConfig(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	var req updateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	for key, entry := range req.Config {
		pc := models.PluginConfig{
			PluginID: id,
			Key:      key,
			Value:    entry.Value,
			IsSecret: entry.IsSecret,
		}
		// Upsert: update if exists, create if not.
		result := h.db.Where("plugin_id = ? AND key = ?", id, key).First(&models.PluginConfig{})
		if result.Error != nil {
			h.db.Create(&pc)
		} else {
			h.db.Model(&models.PluginConfig{}).Where("plugin_id = ? AND key = ?", id, key).Updates(map[string]interface{}{
				"value":     entry.Value,
				"is_secret": entry.IsSecret,
			})
		}
	}

	// Restart plugin if it is currently running.
	if plugin.Enabled && plugin.ContainerID != "" && h.runtime != nil {
		ctx := c.Request.Context()
		_ = h.runtime.StopPlugin(ctx, plugin.ContainerID)

		env := h.buildEnv(id)
		env["ROBOSLOP_KERNEL_HOST"] = h.cfg.Host
		env["ROBOSLOP_KERNEL_PORT"] = h.cfg.Port

		containerID, err := h.runtime.StartPlugin(ctx, &plugin, env)
		if err != nil {
			h.db.Model(&plugin).Updates(map[string]interface{}{
				"container_id": "",
				"status":       "error",
			})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "config updated but restart failed: " + err.Error()})
			return
		}

		h.db.Model(&plugin).Updates(map[string]interface{}{
			"container_id": containerID,
			"status":       "running",
		})
	}

	c.JSON(http.StatusOK, gin.H{"message": "config updated"})
}

// SearchPlugins handles GET /api/plugins/search.
// Only returns plugins that are running and have a reachable host.
func (h *PluginHandler) SearchPlugins(c *gin.Context) {
	capability := c.Query("capability")
	if capability == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "capability query parameter required"})
		return
	}

	var plugins []models.Plugin
	query := h.db.Where("capabilities LIKE ? AND status = ? AND host != ?",
		"%"+strings.ReplaceAll(capability, "%", "\\%")+"%", "running", "")
	if result := query.Find(&plugins); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search plugins"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plugins": plugins})
}

// buildEnv reads PluginConfig rows for a plugin and returns them as a map.
// It also injects default values from the plugin's config_schema for any keys
// that don't have an explicit PluginConfig entry.
func (h *PluginHandler) buildEnv(pluginID string) map[string]string {
	var plugin models.Plugin
	h.db.First(&plugin, "id = ?", pluginID)

	var configs []models.PluginConfig
	h.db.Where("plugin_id = ?", pluginID).Find(&configs)

	env := make(map[string]string, len(configs))
	for _, cfg := range configs {
		env[cfg.Key] = cfg.Value
	}

	// Fill in defaults from config_schema for keys not explicitly set.
	schema, err := plugin.GetConfigSchema()
	if err == nil && schema != nil {
		for key, field := range schema {
			if _, exists := env[key]; !exists && field.Default != "" {
				env[key] = field.Default
			}
		}
	}

	return env
}
