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
// Starts a candidate container alongside the running stable. The candidate is
// isolated — no traffic is routed to it until PromoteCandidate is called.
func (h *PluginHandler) DeployCandidate(c *gin.Context) {
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	id := c.Param("id")

	var req struct {
		Image string `json:"image"` // new image to test as candidate
	}
	_ = c.ShouldBindJSON(&req)

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.IsMetadataOnly() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata-only plugins cannot have candidates"})
		return
	}

	// Use CandidateContainerID (not CandidateHost) to detect an existing candidate —
	// the host is empty until the candidate registers, so HasCandidate() would be false.
	if plugin.CandidateContainerID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "candidate already deployed — promote or rollback first"})
		return
	}

	// Determine candidate image.
	candidateImage := req.Image
	if candidateImage == "" {
		candidateImage = plugin.Image
	}

	ctx := c.Request.Context()

	// Pull image (non-fatal — may be local).
	if err := h.runtime.PullImage(ctx, candidateImage); err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "warning",
			PluginID: id,
			Detail:   "candidate image pull skipped: " + err.Error(),
		})
	}

	// Build env same as stable, plus candidate flag so the SDK registers as candidate.
	env := h.buildEnv(id)
	env["TEAMAGENTICA_KERNEL_HOST"] = h.cfg.AdvertiseHost
	env["TEAMAGENTICA_KERNEL_PORT"] = h.cfg.Port
	env["TEAMAGENTICA_CANDIDATE"] = "true"

	candidatePlugin := plugin
	candidatePlugin.Image = candidateImage

	containerID, err := h.runtime.StartCandidatePlugin(ctx, &candidatePlugin, env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start candidate: " + err.Error()})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"candidate_container_id": containerID,
		"candidate_image":        candidateImage,
		"candidate_version":      "",
		"candidate_host":         "",
		"candidate_port":         0,
		"candidate_event_port":   0,
		"candidate_healthy":      false,
		"candidate_deployed_at":  time.Now(),
		"candidate_last_seen":    time.Time{},
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
// Stops the stable container and makes the candidate the new stable.
// The old stable image/version are saved so RollbackCandidate can restore them.
func (h *PluginHandler) PromoteCandidate(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.CandidateContainerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no candidate deployed"})
		return
	}
	if plugin.CandidateHost == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "candidate not yet registered — it may still be starting"})
		return
	}

	ctx := c.Request.Context()

	// Stop old stable.
	if h.runtime != nil && plugin.ContainerID != "" {
		_ = h.runtime.StopPlugin(ctx, plugin.ContainerID)
	}

	// Promote: candidate → stable, stable → previous.
	h.db.Model(&plugin).Updates(map[string]interface{}{
		// Save old stable for rollback.
		"previous_image":   plugin.Image,
		"previous_version": plugin.Version,
		// Candidate becomes stable.
		"container_id": plugin.CandidateContainerID,
		"image":        plugin.CandidateImage,
		"version":      plugin.CandidateVersion,
		"host":         plugin.CandidateHost,
		"http_port":    plugin.CandidatePort,
		"event_port":   plugin.CandidateEventPort,
		"status":       "running",
		"last_seen":    time.Now(),
		// Clear candidate.
		"candidate_container_id": "",
		"candidate_image":        "",
		"candidate_version":      "",
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
		Detail:   fmt.Sprintf("promoted image=%s previous=%s", plugin.CandidateImage, plugin.Image),
	})

	c.JSON(http.StatusOK, gin.H{"message": "candidate promoted to stable"})
}

// RollbackCandidate handles POST /api/plugins/:id/rollback.
//
// Two scenarios:
//   - Candidate deployed but not yet promoted: stop the candidate, stable keeps running.
//   - Already promoted (previous_image set): stop current stable, restart from previous image.
func (h *PluginHandler) RollbackCandidate(c *gin.Context) {
	id := c.Param("id")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.CandidateContainerID == "" && plugin.PreviousImage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to roll back"})
		return
	}

	ctx := c.Request.Context()

	// Scenario 1: candidate exists but hasn't been promoted yet — just discard it.
	if plugin.CandidateContainerID != "" && plugin.PreviousImage == "" {
		if h.runtime != nil {
			_ = h.runtime.StopPlugin(ctx, plugin.CandidateContainerID)
		}
		h.db.Model(&plugin).Updates(map[string]interface{}{
			"candidate_container_id": "",
			"candidate_image":        "",
			"candidate_version":      "",
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
			Detail:   "candidate discarded — stable unchanged",
		})
		c.JSON(http.StatusOK, gin.H{"message": "candidate discarded"})
		return
	}

	// Scenario 2: already promoted — stop current stable, restart from previous image.
	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime not available"})
		return
	}

	if plugin.ContainerID != "" {
		_ = h.runtime.StopPlugin(ctx, plugin.ContainerID)
	}
	if plugin.CandidateContainerID != "" {
		_ = h.runtime.StopPlugin(ctx, plugin.CandidateContainerID)
	}

	env := h.buildEnv(id)
	env["TEAMAGENTICA_KERNEL_HOST"] = h.cfg.AdvertiseHost
	env["TEAMAGENTICA_KERNEL_PORT"] = h.cfg.Port

	plugin.Image = plugin.PreviousImage
	containerID, err := h.runtime.StartPlugin(ctx, &plugin, env)
	if err != nil {
		h.db.Model(&plugin).Updates(map[string]interface{}{"container_id": "", "status": "error"})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "rollback failed: " + err.Error()})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"image":        plugin.PreviousImage,
		"version":      plugin.PreviousVersion,
		"status":       "running",
		"host":         "",
		"last_seen":    time.Time{},
		"previous_image":         "",
		"previous_version":       "",
		"candidate_container_id": "",
		"candidate_image":        "",
		"candidate_version":      "",
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
		Detail:   fmt.Sprintf("restored image=%s", plugin.PreviousImage),
	})

	c.JSON(http.StatusOK, gin.H{"message": "rolled back to previous version"})
}
