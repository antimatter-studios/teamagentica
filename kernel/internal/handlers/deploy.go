package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// DeployCandidate handles POST /api/plugins/:id/deploy.
// Starts a candidate container alongside the primary.
func (h *PluginHandler) DeployCandidate(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		Image   string `json:"image"`   // new image to deploy (prod candidate)
		DevMode bool   `json:"dev_mode"` // use dev image + source mounts
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.HasCandidate() {
		c.JSON(http.StatusConflict, gin.H{"error": "candidate already deployed — promote or rollback first"})
		return
	}

	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	// Determine candidate image.
	candidateImage := req.Image
	if req.DevMode {
		candidateImage = h.cfg.ResolveImage(plugin.Image)
	}
	if candidateImage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image or dev_mode required"})
		return
	}

	// Build env — same as primary but with candidate flag.
	env := h.buildEnv(id)
	env["TEAMAGENTICA_KERNEL_HOST"] = h.cfg.AdvertiseHost
	env["TEAMAGENTICA_KERNEL_PORT"] = h.cfg.Port
	env["TEAMAGENTICA_CANDIDATE"] = "true"

	// Use a temporary plugin struct for StartPlugin with candidate naming.
	candidatePlugin := plugin
	candidatePlugin.Image = candidateImage

	// Pull image.
	if err := h.runtime.PullImage(c.Request.Context(), candidateImage); err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "warning",
			PluginID: id,
			Detail:   "candidate image pull skipped: " + err.Error(),
		})
	}

	containerID, err := h.runtime.StartCandidatePlugin(c.Request.Context(), &candidatePlugin, env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start candidate: " + err.Error()})
		return
	}

	// Update plugin with candidate fields.
	now := time.Now()
	h.db.Model(&plugin).Updates(map[string]interface{}{
		"candidate_container_id": containerID,
		"candidate_deployed_at":  now,
		"candidate_healthy":      false, // will become true on first heartbeat
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "deploy",
		PluginID: id,
		Detail:   fmt.Sprintf("candidate started: image=%s container=%s", candidateImage, containerID),
	})

	c.JSON(http.StatusOK, gin.H{
		"message":      "candidate deployed",
		"container_id": containerID,
		"image":        candidateImage,
	})
}

// PromoteCandidate handles POST /api/plugins/:id/promote.
// Stops the primary, makes the candidate the new primary.
func (h *PluginHandler) PromoteCandidate(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if !plugin.HasCandidate() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no candidate deployed"})
		return
	}

	// Stop the old primary container.
	if h.runtime != nil && plugin.ContainerID != "" {
		_ = h.runtime.StopPlugin(c.Request.Context(), plugin.ContainerID)
	}

	// Promote candidate to primary.
	h.db.Model(&plugin).Updates(map[string]interface{}{
		// Save previous for potential future rollback info.
		"previous_image":   plugin.Image,
		"previous_version": plugin.Version,
		// Candidate becomes primary.
		"host":       plugin.CandidateHost,
		"http_port":  plugin.CandidatePort,
		"event_port": plugin.CandidateEventPort,
		"container_id": plugin.CandidateContainerID,
		"status":     "running",
		"last_seen":  time.Now(),
		// Clear candidate fields.
		"candidate_container_id": "",
		"candidate_host":         "",
		"candidate_port":         0,
		"candidate_event_port":   0,
		"candidate_healthy":      false,
		"candidate_deployed_at":  time.Time{},
		"candidate_last_seen":    time.Time{},
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "promote",
		PluginID: id,
		Detail:   "candidate promoted to primary",
	})

	c.JSON(http.StatusOK, gin.H{"message": "candidate promoted to primary"})
}

// RollbackCandidate handles POST /api/plugins/:id/rollback.
// Stops the candidate, traffic returns to primary.
func (h *PluginHandler) RollbackCandidate(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if !plugin.HasCandidate() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no candidate deployed"})
		return
	}

	// Stop candidate container.
	if h.runtime != nil && plugin.CandidateContainerID != "" {
		_ = h.runtime.StopPlugin(c.Request.Context(), plugin.CandidateContainerID)
	}

	// Clear candidate fields — traffic falls back to primary automatically.
	plugin.ClearCandidate()
	h.db.Model(&models.Plugin{}).Where("id = ?", id).Updates(map[string]interface{}{
		"candidate_container_id": "",
		"candidate_host":         "",
		"candidate_port":         0,
		"candidate_event_port":   0,
		"candidate_healthy":      false,
		"candidate_deployed_at":  time.Time{},
		"candidate_last_seen":    time.Time{},
	})

	h.Events.Emit(events.DebugEvent{
		Type:     "rollback",
		PluginID: id,
		Detail:   "candidate rolled back — traffic returns to primary",
	})

	c.JSON(http.StatusOK, gin.H{"message": "candidate rolled back"})
}
