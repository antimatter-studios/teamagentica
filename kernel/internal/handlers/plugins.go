package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/config"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// PluginHandler holds dependencies for plugin management endpoints.
type PluginHandler struct {
	runtime   runtime.ContainerRuntime
	cfg       *config.Config
	clientTLS *tls.Config
	transport *http.Transport // shared transport for all outbound plugin requests
	Events    *events.Hub
	Subs      *events.SubscriptionManager
}

func (h *PluginHandler) db() *gorm.DB { return database.Get() }

// NewPluginHandler creates a new PluginHandler.
// clientTLS is optional; pass nil to disable mTLS for proxied requests.
func NewPluginHandler(rt runtime.ContainerRuntime, cfg *config.Config, clientTLS *tls.Config) *PluginHandler {
	t := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	if clientTLS != nil {
		t.TLSClientConfig = clientTLS
	}
	return &PluginHandler{runtime: rt, cfg: cfg, clientTLS: clientTLS, transport: t, Events: events.NewHub(), Subs: events.NewPersistentSubscriptionManager()}
}

// stripHTMLTags removes all HTML tags from a string to prevent XSS.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTMLTags(s string) string {
	return strings.TrimSpace(htmlTagRe.ReplaceAllString(s, ""))
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
	if err := h.db().First(&existing, "id = ?", req.ID).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plugin already registered"})
		return
	}

	plugin := models.Plugin{
		ID:       req.ID,
		Name:     stripHTMLTags(req.Name),
		Version:  req.Version,
		Image:    req.Image,
		GRPCPort: req.GRPCPort,
		HTTPPort: req.HTTPPort,
	}
	plugin.SetCapabilities(req.Capabilities)

	if req.ConfigSchema != nil {
		plugin.ConfigSchema = models.JSONRawString(req.ConfigSchema)
	}

	if result := h.db().Create(&plugin); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register plugin"})
		return
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "install",
		PluginID: req.ID,
		Detail:   fmt.Sprintf("image=%s version=%s", req.Image, req.Version),
	})

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.install",
			"plugin:"+req.ID,
			fmt.Sprintf(`{"name":%q,"version":%q}`, req.Name, req.Version),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusCreated, gin.H{"plugin": plugin})
}

// ListPlugins handles GET /api/plugins.
func (h *PluginHandler) ListPlugins(c *gin.Context) {
	var plugins []models.Plugin
	if result := h.db().Find(&plugins); result.Error != nil {
		database.CheckError(result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch plugins"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plugins": plugins})
}

// GetPlugin handles GET /api/plugins/:id.
func (h *PluginHandler) GetPlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"plugin": plugin})
}

// UninstallPlugin handles DELETE /api/plugins/:id.
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	if plugin.System {
		c.JSON(http.StatusForbidden, gin.H{"error": "system plugins cannot be uninstalled"})
		return
	}

	// Stop container if running (StopPlugin handles vanished containers gracefully).
	if plugin.ContainerID != "" && h.runtime != nil {
		if err := h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID); err != nil {
			h.Events.Emit(events.DebugEvent{
				Type:     "error",
				PluginID: id,
				Detail:   "failed to stop container during uninstall: " + err.Error(),
			})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop plugin: " + err.Error()})
			return
		}
	}

	// Stop any managed containers owned by this plugin.
	h.StopManagedContainersByPlugin(c.Request.Context(), id)

	// Remove config entries.
	h.db().Where("owner_id = ?", id).Delete(&models.Config{})

	// Remove aliases owned by this plugin.
	h.db().Where("plugin_id = ?", id).Delete(&models.Alias{})

	// Remove plugin record (data volume is kept).
	if result := h.db().Delete(&plugin); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete plugin"})
		return
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "uninstall",
		PluginID: id,
	})

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.uninstall", "plugin:"+id, "", c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "plugin uninstalled"})
}

// EnablePlugin handles POST /api/plugins/:id/enable.
func (h *PluginHandler) EnablePlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	if h.runtime == nil && !plugin.IsMetadataOnly() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	var allEnabled []string
	visited := map[string]bool{}
	if err := h.enablePlugin(c.Request.Context(), &plugin, visited, &allEnabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enable plugin: " + err.Error()})
		return
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.enable", "plugin:"+id, "", c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "plugin enabled", "enabled": allEnabled})
}

// enablePlugin is the single enable path for both user-requested and
// dependency plugins. The visited map prevents infinite dependency loops.
func (h *PluginHandler) enablePlugin(ctx context.Context, plugin *models.Plugin, visited map[string]bool, allEnabled *[]string) error {
	if visited[plugin.ID] {
		return nil
	}
	visited[plugin.ID] = true

	// Already running with a live container — nothing to do.
	// If enabled=true but container_id is empty, the container failed to start
	// previously and we should retry rather than silently skip it.
	if plugin.Enabled && plugin.ContainerID != "" {
		return nil
	}

	// Recursively enable dependencies first.
	for _, cap := range plugin.GetDependencies() {
		var allPlugins []models.Plugin
		h.db().Find(&allPlugins)
		var dep *models.Plugin
		for i := range allPlugins {
			for _, c := range allPlugins[i].GetCapabilities() {
				if c == cap {
					dep = &allPlugins[i]
					break
				}
			}
			if dep != nil {
				break
			}
		}
		if dep == nil {
			return fmt.Errorf("no installed plugin provides capability %q", cap)
		}
		if err := h.enablePlugin(ctx, dep, visited, allEnabled); err != nil {
			return fmt.Errorf("dependency %s: %w", dep.ID, err)
		}
	}

	// Metadata-only plugins have no runtime — just mark enabled.
	if plugin.IsMetadataOnly() {
		h.db().Model(plugin).Updates(map[string]interface{}{"status": "enabled", "enabled": true})
		h.Events.Emit(events.DebugEvent{
			Type:     "enable",
			PluginID: plugin.ID,
			Detail:   "metadata-only plugin enabled",
		})
		*allEnabled = append(*allEnabled, plugin.ID)
		return nil
	}

	env := map[string]string{
		"TEAMAGENTICA_KERNEL_HOST": h.cfg.AdvertiseHost,
		"TEAMAGENTICA_KERNEL_PORT": h.cfg.TLSPort,
	}

	// Pull image (non-fatal — image may be local-only).
	if err := h.runtime.PullImage(ctx, plugin.Image); err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "warning",
			PluginID: plugin.ID,
			Detail:   "image pull skipped (may be local): " + err.Error(),
		})
	}

	containerID, err := h.runtime.StartPlugin(ctx, plugin, env)
	if err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "error",
			PluginID: plugin.ID,
			Detail:   "start failed: " + err.Error(),
		})
		return fmt.Errorf("start %s: %w", plugin.ID, err)
	}

	h.db().Model(plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"status":       "running",
		"enabled":      true,
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "enable",
		PluginID: plugin.ID,
		Detail:   "container=" + containerID,
	})

	*allEnabled = append(*allEnabled, plugin.ID)
	log.Printf("plugins: enabled %s (container=%s)", plugin.ID, containerID[:12])
	return nil
}

// DisablePlugin handles POST /api/plugins/:id/disable.
func (h *PluginHandler) DisablePlugin(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	if plugin.System {
		c.JSON(http.StatusForbidden, gin.H{"error": "system plugins cannot be disabled"})
		return
	}

	if plugin.ContainerID != "" && h.runtime != nil {
		if err := h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID); err != nil {
			// StopPlugin already handles not-found (container vanished) gracefully.
			// Any error reaching here is a real Docker problem.
			h.Events.Emit(events.DebugEvent{
				Type:     "error",
				PluginID: id,
				Detail:   "failed to stop container: " + err.Error(),
			})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop plugin: " + err.Error()})
			return
		}
	}

	// Stop any managed containers owned by this plugin.
	h.StopManagedContainersByPlugin(c.Request.Context(), id)

	h.db().Model(&plugin).Updates(map[string]interface{}{
		"container_id": "",
		"status":       "stopped",
		"enabled":      false,
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "disable",
		PluginID: id,
	})

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.disable", "plugin:"+id, "", c.ClientIP(), true)
	}

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
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	// Metadata-only plugins have no container to restart.
	if plugin.IsMetadataOnly() {
		c.JSON(http.StatusOK, gin.H{"message": "metadata-only plugin, nothing to restart"})
		return
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "restart",
		PluginID: id,
		Detail:   "user-initiated restart",
	})

	// Stop existing container if running.
	if plugin.ContainerID != "" {
		h.Events.Emit(events.DebugEvent{
			Type:     "stop",
			PluginID: id,
			Detail:   "stopping container=" + plugin.ContainerID[:12],
		})
		_ = h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID)
	}

	env := map[string]string{
		"TEAMAGENTICA_KERNEL_HOST": h.cfg.AdvertiseHost,
		"TEAMAGENTICA_KERNEL_PORT": h.cfg.TLSPort,
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "start",
		PluginID: id,
		Detail:   "starting container image=" + plugin.Image,
	})
	containerID, err := h.runtime.StartPlugin(c.Request.Context(), &plugin, env)
	if err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "error",
			PluginID: id,
			Detail:   "restart failed: " + err.Error(),
		})
		h.db().Model(&plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "error",
		})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restart plugin: " + err.Error()})
		return
	}

	h.db().Model(&plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"status":       "running",
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "restart",
		PluginID: id,
		Detail:   "running container=" + containerID[:12],
	})

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.restart", "plugin:"+id, "", c.ClientIP(), true)
	}

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
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
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

	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(logs))
}

// GetKernelLogs handles GET /api/kernel/logs.
// Returns the kernel container's own logs.
func (h *PluginHandler) GetKernelLogs(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	selfID := h.runtime.SelfContainerID()
	if selfID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "kernel container ID not discovered"})
		return
	}

	tail := 100
	if t := c.Query("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 {
			tail = v
		}
	}

	logs, err := h.runtime.ContainerLogs(c.Request.Context(), selfID, tail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get kernel logs: " + err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(logs))
}

// GetUILogs handles GET /api/ui/logs.
// Returns logs from the web-dashboard container.
func (h *PluginHandler) GetUILogs(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	tail := 100
	if t := c.Query("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 {
			tail = v
		}
	}

	cid, err := h.runtime.UIContainerID(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "UI container not found: " + err.Error()})
		return
	}

	logs, err := h.runtime.ContainerLogs(c.Request.Context(), cid, tail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get UI logs: " + err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(logs))
}

// GetSelfConfig handles GET /api/plugins/:id/self-config — called by plugins
// via the SDK to fetch their own configuration. Secret values are returned
// unmasked because the plugin needs the actual values (e.g. API keys).
// This endpoint is behind PluginTokenAuth so only authenticated plugins can call it.
func (h *PluginHandler) GetSelfConfig(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	var configs []models.Config
	h.db().Where("owner_id = ?", id).Find(&configs)

	result := make(map[string]string, len(configs))
	for _, cfg := range configs {
		result[cfg.Key] = cfg.Value
	}

	// Config defaults are handled by the plugin itself — it knows its own schema.
	// We only return explicitly-set values here.

	c.JSON(http.StatusOK, gin.H{"config": result})
}

// GetPluginConfig handles GET /api/plugins/:id/config.
func (h *PluginHandler) GetPluginConfig(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	var configs []models.Config
	h.db().Where("owner_id = ?", id).Find(&configs)

	// Build map of stored values.
	stored := make(map[string]models.Config, len(configs))
	for _, cfg := range configs {
		stored[cfg.Key] = cfg
	}

	type configItem struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
		Default  string `json:"default,omitempty"`
		Label    string `json:"label,omitempty"`
		Required bool   `json:"required,omitempty"`
		ReadOnly bool   `json:"readonly,omitempty"`
	}

	// Use DB-cached schema (pushed by plugin on every startup) as primary source.
	// Fall back to live fetch for plugins that predate schema caching.
	schema, _ := plugin.GetConfigSchema()
	if schema == nil && plugin.Status == "running" && plugin.Host != "" {
		if schemaBody, err := h.fetchPluginSchema(plugin); err == nil {
			var full struct {
				Config map[string]models.ConfigSchemaField `json:"config"`
			}
			if json.Unmarshal(schemaBody, &full) == nil && full.Config != nil {
				schema = full.Config
			}
		}
	}

	var items []configItem

	if schema != nil {
		// Enumerate all schema fields, merged with stored values.
		// Skip readonly fields — they are internal, not user-settable.
		// Sort by Order field to preserve logical grouping defined in the manifest.
		type schemaEntry struct {
			key   string
			field models.ConfigSchemaField
		}
		entries := make([]schemaEntry, 0, len(schema))
		for k, f := range schema {
			entries = append(entries, schemaEntry{k, f})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].field.Order != entries[j].field.Order {
				return entries[i].field.Order < entries[j].field.Order
			}
			return entries[i].key < entries[j].key
		})

		seen := make(map[string]bool, len(entries))
		for _, e := range entries {
			key, field := e.key, e.field
			if field.ReadOnly {
				continue
			}
			seen[key] = true
			val := ""
			isSecret := field.Secret
			if field.Virtual {
				// Virtual fields are never persisted — always show the default.
				val = field.Default
			} else if cfg, ok := stored[key]; ok {
				if cfg.IsSecret || isSecret {
					if cfg.Value != "" {
						val = "********"
					}
				} else {
					val = cfg.Value
				}
			}
			items = append(items, configItem{
				Key:      key,
				Value:    val,
				IsSecret: isSecret,
				Default:  field.Default,
				Label:    field.Label,
				Required: field.Required,
			})
		}
		// Include any stored values not present in schema.
		for key, cfg := range stored {
			if seen[key] {
				continue
			}
			val := cfg.Value
			if cfg.IsSecret {
				val = "********"
			}
			items = append(items, configItem{Key: key, Value: val, IsSecret: cfg.IsSecret})
		}
	} else {
		// No schema — fall back to stored values only.
		for _, cfg := range configs {
			val := cfg.Value
			if cfg.IsSecret {
				val = "********"
			}
			items = append(items, configItem{Key: cfg.Key, Value: val, IsSecret: cfg.IsSecret})
		}
	}

	c.JSON(http.StatusOK, gin.H{"config": items})
}

// UpdatePluginConfig handles PUT /api/plugins/:id/config.
func (h *PluginHandler) UpdatePluginConfig(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	var req updateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch live schema from plugin to check for readonly/virtual fields.
	var skipPersist map[string]bool
	if plugin.Status == "running" && plugin.Host != "" {
		if schemaBody, err := h.fetchPluginSchema(plugin); err == nil {
			var schema struct {
				Config map[string]struct {
					ReadOnly bool `json:"readonly"`
					Virtual  bool `json:"virtual"`
				} `json:"config"`
			}
			if json.Unmarshal(schemaBody, &schema) == nil {
				skipPersist = make(map[string]bool)
				for k, f := range schema.Config {
					if f.ReadOnly || f.Virtual {
						skipPersist[k] = true
					}
				}
			}
		}
	}

	const maxConfigValueSize = 128 * 1024 // 128 KB

	for key, entry := range req.Config {
		// Skip readonly/virtual fields — don't persist to DB.
		// Virtual fields are still included in the config:update event.
		if skipPersist[key] {
			continue
		}

		if len(entry.Value) > maxConfigValueSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("config value for %q exceeds maximum size (%d bytes)", key, maxConfigValueSize)})
			return
		}

		pc := models.Config{
			OwnerID:  id,
			Key:      key,
			Value:    entry.Value,
			IsSecret: entry.IsSecret,
		}
		// Upsert: update if exists, create if not.
		result := h.db().Where("owner_id = ? AND key = ?", id, key).First(&models.Config{})
		if result.Error != nil {
			if res := h.db().Create(&pc); res.Error != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config key %q: %v", key, res.Error)})
				return
			}
		} else {
			if res := h.db().Model(&models.Config{}).Where("owner_id = ? AND key = ?", id, key).Updates(map[string]interface{}{
				"value":     entry.Value,
				"is_secret": entry.IsSecret,
			}); res.Error != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update config key %q: %v", key, res.Error)})
				return
			}
		}
	}

	// Emit config:update event to the running plugin.
	if plugin.Status == "running" && plugin.Host != "" {
		var keys []string
		for k := range req.Config {
			keys = append(keys, k)
		}
		keysJSON, _ := json.Marshal(keys)

		// Build the new config values for the event detail.
		configValues := make(map[string]string, len(req.Config))
		for k, v := range req.Config {
			configValues[k] = v.Value
		}

		detail, _ := json.Marshal(map[string]interface{}{
			"keys":   keys,
			"config": configValues,
		})

		h.Events.Emit(events.DebugEvent{
			Type:     "config:update",
			PluginID: id,
			Detail:   fmt.Sprintf("config update keys=%s", keysJSON),
		})

		h.handleAddressedEvent("kernel", "config:update", string(detail), id, time.Now())
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		// List updated keys (don't log values since they could be secrets).
		var keys []string
		for k := range req.Config {
			keys = append(keys, k)
		}
		keysJSON, _ := json.Marshal(keys)
		al.LogUserAction(uid, "plugin.config_update", "plugin:"+id,
			fmt.Sprintf(`{"keys":%s}`, keysJSON), c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "config updated"})
}

// DeletePluginConfigKey handles DELETE /api/plugins/:id/config/:key.
// Removes a single config key from the plugin's stored configuration.
func (h *PluginHandler) DeletePluginConfigKey(c *gin.Context) {
	id := c.Param("id")
	key := c.Param("key")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	result := h.db().Where("owner_id = ? AND key = ?", id, key).Delete(&models.Config{})
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "config key not found"})
		return
	}

	if al := getAudit(c); al != nil {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(uint)
		al.LogUserAction(uid, "plugin.config_delete", "plugin:"+id,
			fmt.Sprintf(`{"key":%q}`, key), c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, gin.H{"message": "config key deleted"})
}

// SearchPlugins handles GET /api/plugins/search?capability=<prefix>.
// Returns enabled plugins whose capabilities match the given prefix.
// Accessible by both admin users and plugin service tokens.
func (h *PluginHandler) SearchPlugins(c *gin.Context) {
	capability := c.Query("capability")
	if capability == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "capability query parameter required"})
		return
	}

	var allPlugins []models.Plugin
	if err := h.db().Where("enabled = ?", true).Find(&allPlugins).Error; err != nil {
		database.CheckError(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search plugins"})
		return
	}

	var matched []models.Plugin
	for _, p := range allPlugins {
		for _, cap := range p.GetCapabilities() {
			if strings.HasPrefix(cap, capability) {
				matched = append(matched, p)
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"plugins": matched})
}

// GetPluginSchema handles GET /api/plugins/:id/schema — proxies to the running
// plugin's internal server to fetch its live schema. No schema is stored in the DB.
func (h *PluginHandler) GetPluginSchema(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		msg := "plugin not running"
		if plugin.Status == "running" && plugin.Host == "" {
			msg = "plugin is starting — container is up but not yet registered with kernel"
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": msg})
		return
	}

	schemaBody, err := h.fetchPluginSchema(plugin)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch schema from plugin: " + err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json", schemaBody)
}

// GetPluginSchemaSection handles GET /api/plugins/:id/schema/:section — returns
// a single section (e.g. "config", "workspace") from the plugin's live schema.
func (h *PluginHandler) GetPluginSchemaSection(c *gin.Context) {
	id := c.Param("id")
	section := c.Param("section")

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", id)})
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin not running"})
		return
	}

	schemaBody, err := h.fetchPluginSchema(plugin)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch schema from plugin: " + err.Error()})
		return
	}

	var schema map[string]json.RawMessage
	if err := json.Unmarshal(schemaBody, &schema); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid schema JSON from plugin"})
		return
	}

	sectionData, ok := schema[section]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("schema section %q not found", section)})
		return
	}

	c.Data(http.StatusOK, "application/json", sectionData)
}

// fetchPluginSchema calls GET /schema on the plugin's internal server (event_port)
// and returns the raw response body. Falls back to http_port if event_port is 0.
func (h *PluginHandler) fetchPluginSchema(plugin models.Plugin) ([]byte, error) {
	port := plugin.EventPort
	if port == 0 {
		port = plugin.HTTPPort
	}

	url := fmt.Sprintf("%s://%s:%d/schema", h.pluginScheme(), plugin.Host, port)
	client := &http.Client{Timeout: 5 * time.Second, Transport: h.transport}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}


