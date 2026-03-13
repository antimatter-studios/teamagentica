package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Tools returns tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": []gin.H{
		{
			"name":        "list_environments",
			"description": "List available workspace environment types. Returns environment IDs, names, and descriptions. Use this to find valid environment_id values for create_workspace.",
			"endpoint":    "/tool/list_environments",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		{
			"name":        "create_workspace",
			"description": "Create a new development workspace. Launches a container with the specified environment type. Optionally clones a git repository. Returns workspace details including access URL.",
			"endpoint":    "/tool/create_workspace",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":           gin.H{"type": "string", "description": "Display name for the workspace"},
					"environment_id": gin.H{"type": "string", "description": "Environment plugin ID (get from list_environments)"},
					"git_repo":       gin.H{"type": "string", "description": "Git repository URL to clone into the workspace"},
					"git_ref":        gin.H{"type": "string", "description": "Git branch or tag to checkout after cloning"},
					"volume_name":    gin.H{"type": "string", "description": "Reuse an existing volume instead of creating a new one"},
				},
				"required": []string{"name", "environment_id"},
			},
		},
		{
			"name":        "list_workspaces",
			"description": "List all workspaces with their current status, URLs, and environment info.",
			"endpoint":    "/tool/list_workspaces",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		{
			"name":        "start_workspace",
			"description": "Start a stopped workspace container. Use list_workspaces first to find the workspace ID.",
			"endpoint":    "/tool/start_workspace",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"workspace_id": gin.H{"type": "string", "description": "ID of the workspace to start"},
				},
				"required": []string{"workspace_id"},
			},
		},
		{
			"name":        "rename_workspace",
			"description": "Rename an existing workspace. Updates the display name and volume directory slug. Use list_workspaces first to find the workspace ID.",
			"endpoint":    "/tool/rename_workspace",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"workspace_id": gin.H{"type": "string", "description": "ID of the workspace to rename"},
					"name":         gin.H{"type": "string", "description": "New display name for the workspace"},
				},
				"required": []string{"workspace_id", "name"},
			},
		},
	}})
}

// ToolListEnvironments handles POST /tool/list_environments.
func (h *Handler) ToolListEnvironments(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	plugins, err := h.sdk.SearchPlugins("workspace:environment")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to discover environments"})
		return
	}

	var envs []gin.H
	for _, p := range plugins {
		ws := h.fetchWorkspaceSchema(p.ID)
		if ws == nil {
			continue
		}
		envs = append(envs, gin.H{
			"environment_id": p.ID,
			"name":           ws.DisplayName,
			"description":    ws.Description,
		})
	}

	if envs == nil {
		envs = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"environments": envs})
}

// ToolCreateWorkspace handles POST /tool/create_workspace.
// Delegates to the existing CreateWorkspace handler by forwarding the JSON body.
func (h *Handler) ToolCreateWorkspace(c *gin.Context) {
	h.CreateWorkspace(c)
}

// ToolListWorkspaces handles POST /tool/list_workspaces.
func (h *Handler) ToolListWorkspaces(c *gin.Context) {
	h.ListWorkspaces(c)
}

// ToolStartWorkspace handles POST /tool/start_workspace.
func (h *Handler) ToolStartWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	var req struct {
		WorkspaceID string `json:"workspace_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id is required"})
		return
	}

	mc, err := h.sdk.StartManagedContainer(req.WorkspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start workspace: " + err.Error()})
		return
	}

	result := workspaceInfo{
		ID:         mc.ID,
		Name:       mc.Name,
		Status:     mc.Status,
		Subdomain:  mc.Subdomain,
		VolumeName: mc.VolumeName,
	}
	if rec, err := h.db.GetByContainerID(mc.ID); err == nil {
		result.Environment = rec.EnvironmentID
	}
	if mc.Subdomain != "" && h.baseDomain != "" {
		result.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	c.JSON(http.StatusOK, result)
}

// ToolRenameWorkspace handles POST /tool/rename_workspace.
func (h *Handler) ToolRenameWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	var req struct {
		WorkspaceID string `json:"workspace_id" binding:"required"`
		Name        string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and name are required"})
		return
	}

	newDisplayName := strings.TrimSpace(req.Name)
	if newDisplayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	newSlug := slugify(newDisplayName)
	if newSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain at least one alphanumeric character"})
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var found *pluginsdk.ManagedContainerInfo
	for _, mc := range containers {
		if mc.ID == req.WorkspaceID {
			found = &mc
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	wsPrefix := extractVolumePrefix(found.VolumeName)
	newVolumeName := wsPrefix + newSlug

	if newVolumeName != found.VolumeName {
		oldPath := filepath.Join(h.workspaceDir, "volumes", found.VolumeName)
		newPath := filepath.Join(h.workspaceDir, "volumes", newVolumeName)

		if _, err := os.Stat(newPath); err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "a volume with that name already exists"})
			return
		}

		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename volume: " + err.Error()})
				return
			}
		}

		_, err = h.sdk.UpdateManagedContainer(req.WorkspaceID, pluginsdk.UpdateManagedContainerRequest{
			Name:       &newDisplayName,
			VolumeName: &newVolumeName,
		})
		if err != nil {
			os.Rename(
				filepath.Join(h.workspaceDir, "volumes", newVolumeName),
				filepath.Join(h.workspaceDir, "volumes", found.VolumeName),
			)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workspace: " + err.Error()})
			return
		}
	} else {
		_, err = h.sdk.UpdateManagedContainer(req.WorkspaceID, pluginsdk.UpdateManagedContainerRequest{
			Name: &newDisplayName,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename workspace: " + err.Error()})
			return
		}
	}

	h.emitEvent("workspace:renamed", fmt.Sprintf(`{"id":"%s","name":"%s"}`, req.WorkspaceID, newDisplayName))
	c.JSON(http.StatusOK, gin.H{"status": "renamed", "id": req.WorkspaceID, "name": newDisplayName})
}
