package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// --- request types ---

type createManagedContainerRequest struct {
	Name       string            `json:"name" binding:"required"`
	Image      string            `json:"image" binding:"required"`
	Port       int               `json:"port" binding:"required"`
	Subdomain  string            `json:"subdomain" binding:"required"`
	VolumeName string            `json:"volume_name"`
	Env        map[string]string `json:"env"`
	Cmd        []string          `json:"cmd"`
	DockerUser string            `json:"docker_user"`
}

// --- helpers ---

// extractPluginID returns the plugin ID from the service token in context.
// Service tokens have Email = "service:{pluginID}".
func extractPluginID(c *gin.Context) (string, bool) {
	claimsVal, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no claims in context"})
		return "", false
	}
	claims, ok := claimsVal.(*auth.Claims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid claims"})
		return "", false
	}
	if !strings.HasPrefix(claims.Email, "service:") {
		c.JSON(http.StatusForbidden, gin.H{"error": "not a service token"})
		return "", false
	}
	return strings.TrimPrefix(claims.Email, "service:"), true
}

// --- plugin-callable handlers (PluginTokenAuth) ---

// CreateManagedContainer handles POST /api/plugins/containers.
func (h *PluginHandler) CreateManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var req createManagedContainerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check subdomain uniqueness.
	var existing models.ManagedContainer
	if err := h.db.Where("subdomain = ?", req.Subdomain).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subdomain %q already in use", req.Subdomain)})
		return
	}

	mc := models.ManagedContainer{
		ID:         uuid.New().String()[:8],
		PluginID:   pluginID,
		Name:       req.Name,
		Image:      req.Image,
		Port:       req.Port,
		Subdomain:  req.Subdomain,
		VolumeName: req.VolumeName,
		DockerUser: req.DockerUser,
	}
	mc.SetEnv(req.Env)
	mc.SetCmd(req.Cmd)

	if err := h.db.Create(&mc).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save container record"})
		return
	}

	// Start the container via Docker runtime.
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime unavailable"})
		return
	}

	containerID, err := h.runtime.StartManagedContainer(c.Request.Context(), &mc, h.cfg.BaseDomain)
	if err != nil {
		h.db.Delete(&mc)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start container: %v", err)})
		return
	}

	mc.ContainerID = containerID
	mc.Status = "running"
	h.db.Save(&mc)

	c.JSON(http.StatusCreated, mc)
}

// ListManagedContainers handles GET /api/plugins/containers.
func (h *PluginHandler) ListManagedContainers(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var containers []models.ManagedContainer
	h.db.Where("plugin_id = ?", pluginID).Find(&containers)
	c.JSON(http.StatusOK, containers)
}

// GetManagedContainer handles GET /api/plugins/containers/:id.
func (h *PluginHandler) GetManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db.First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found"})
		return
	}
	c.JSON(http.StatusOK, mc)
}

// DeleteManagedContainer handles DELETE /api/plugins/containers/:id.
func (h *PluginHandler) DeleteManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db.First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found"})
		return
	}

	// Stop Docker container.
	if h.runtime != nil && mc.ContainerID != "" {
		if err := h.runtime.StopPlugin(c.Request.Context(), mc.ContainerID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stop container: %v", err)})
			return
		}
	}

	h.db.Delete(&mc)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// UpdateManagedContainer handles PATCH /api/plugins/containers/:id.
// Allows renaming (name, subdomain, volume_name) without stopping the container.
func (h *PluginHandler) UpdateManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db.First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found"})
		return
	}

	var req struct {
		Name       *string `json:"name"`
		Subdomain  *string `json:"subdomain"`
		VolumeName *string `json:"volume_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}

	if req.Name != nil {
		updates["name"] = *req.Name
	}

	if req.Subdomain != nil {
		// Check uniqueness.
		var existing models.ManagedContainer
		if err := h.db.Where("subdomain = ? AND id != ?", *req.Subdomain, mc.ID).First(&existing).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subdomain %q already in use", *req.Subdomain)})
			return
		}
		updates["subdomain"] = *req.Subdomain
	}

	if req.VolumeName != nil {
		updates["volume_name"] = *req.VolumeName
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}

	h.db.Model(&mc).Updates(updates)
	h.db.First(&mc, "id = ?", mc.ID)
	c.JSON(http.StatusOK, mc)
}

// GetManagedContainerLogs handles GET /api/plugins/containers/:id/logs.
func (h *PluginHandler) GetManagedContainerLogs(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db.First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found"})
		return
	}

	if h.runtime == nil || mc.ContainerID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no container to read logs from"})
		return
	}

	logs, err := h.runtime.ContainerLogs(c.Request.Context(), mc.ContainerID, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// --- admin-callable handlers (AuthRequired + plugins:manage) ---

// ListAllManagedContainers handles GET /api/managed-containers.
func (h *PluginHandler) ListAllManagedContainers(c *gin.Context) {
	var containers []models.ManagedContainer
	h.db.Find(&containers)
	c.JSON(http.StatusOK, containers)
}

// ForceDeleteManagedContainer handles DELETE /api/managed-containers/:id.
func (h *PluginHandler) ForceDeleteManagedContainer(c *gin.Context) {
	var mc models.ManagedContainer
	if err := h.db.First(&mc, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found"})
		return
	}

	if h.runtime != nil && mc.ContainerID != "" {
		_ = h.runtime.StopPlugin(c.Request.Context(), mc.ContainerID)
	}

	h.db.Delete(&mc)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// StopManagedContainersByPlugin stops and deletes all managed containers
// owned by the given plugin. Called during plugin disable/uninstall.
func (h *PluginHandler) StopManagedContainersByPlugin(ctx context.Context, pluginID string) {
	var containers []models.ManagedContainer
	h.db.Where("plugin_id = ?", pluginID).Find(&containers)

	for _, mc := range containers {
		if h.runtime != nil && mc.ContainerID != "" {
			_ = h.runtime.StopPlugin(ctx, mc.ContainerID)
		}
		h.db.Delete(&mc)
	}
}
