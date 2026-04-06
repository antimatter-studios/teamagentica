package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/workspace-manager/internal/storage"
)

// randomID generates an 8-character hex string for use as a workspace identifier.
func randomID() string {
	b := make([]byte, 4) // 8 hex chars
	rand.Read(b)
	return hex.EncodeToString(b)
}

type Handler struct {
	baseDomain string
	debug      bool
	sdk        *pluginsdk.Client
	db         *storage.DB
}

func NewHandler(baseDomain string, debug bool, db *storage.DB) *Handler {
	return &Handler{
		baseDomain: baseDomain,
		debug:      debug,
		db:         db,
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
}

// ensureDisk calls storage-disk's API via P2P routing to get-or-create a disk.
// Returns the disk ID (stable over renames) and the internal path.
func (h *Handler) ensureDisk(ctx context.Context, name, diskType string) (diskID string, path string, err error) {
	body, _ := json.Marshal(map[string]string{"name": name, "type": diskType})
	data, err := h.sdk.RouteToPlugin(ctx, "storage-disk", "POST", "/disks", bytes.NewReader(body))
	if err != nil {
		// RouteToPlugin may return an error wrapping a 409 — try to parse it.
		// If the error message contains "409" or "already exists", fall back to GET.
		if !strings.Contains(err.Error(), "409") && !strings.Contains(err.Error(), "already exists") {
			return "", "", fmt.Errorf("create disk %s: %w", name, err)
		}
		// Disk already exists — fetch by name.
		data, err = h.sdk.RouteToPlugin(ctx, "storage-disk", "GET", fmt.Sprintf("/disks/%s/%s", diskType, name), nil)
		if err != nil {
			return "", "", fmt.Errorf("get existing disk %s: %w", name, err)
		}
	}

	var result struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", "", fmt.Errorf("decode disk response: %w", err)
	}
	return result.ID, result.Path, nil
}

// resolveWorkspaceDiskPath finds the workspace-type disk in DiskMounts and resolves
// its host path for git operations. Returns the cross-mounted path accessible from
// this container (storage-root is cross-mounted from storage-disk).
func (h *Handler) resolveWorkspaceDiskPath(ctx context.Context, diskMounts []pluginsdk.DiskMount) (string, error) {
	for _, dm := range diskMounts {
		if dm.DiskType == "workspace" && dm.DiskID != "" {
			data, err := h.sdk.RouteToPlugin(ctx, "storage-disk", "GET", fmt.Sprintf("/disks/by-id/%s", dm.DiskID), nil)
			if err != nil {
				return "", fmt.Errorf("resolve disk %s: %w", dm.DiskID, err)
			}
			var result struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				return "", fmt.Errorf("decode disk response: %w", err)
			}
			// storage-disk returns e.g. "/data/storage-root/workspace/ws-abc123-slug"
			// workspace-manager has /storage-root cross-mounted, so strip "/data"
			return strings.Replace(result.Path, "/data/", "/", 1), nil
		}
	}
	return "", fmt.Errorf("no workspace disk found in mounts")
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"plugin":  "workspace-manager",
		"version": "1.0.0",
	})
}

// --- Environment discovery ---

type environmentInfo struct {
	PluginID    string `json:"plugin_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
	Port        int    `json:"port"`
	Icon        string `json:"icon,omitempty"`
}

// ListEnvironments returns all registered workspace environments from the local DB.
func (h *Handler) ListEnvironments(c *gin.Context) {
	recs, err := h.db.ListEnvironments()
	if err != nil {
		log.Printf("failed to list environments from DB: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list environments"})
		return
	}

	envs := make([]environmentInfo, 0, len(recs))
	for _, r := range recs {
		envs = append(envs, environmentInfo{
			PluginID:    r.PluginID,
			Name:        r.DisplayName,
			Description: r.Description,
			Image:       r.Image,
			Port:        r.Port,
			Icon:        r.Icon,
		})
	}
	c.JSON(http.StatusOK, gin.H{"environments": envs})
}

// --- Workspace CRUD ---

type workspaceInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	EnvironmentName string `json:"environment_name,omitempty"`
	Status          string `json:"status"`
	Subdomain       string `json:"subdomain"`
	URL             string `json:"url,omitempty"`
}

// ListWorkspaces returns all managed containers owned by this plugin.
func (h *Handler) ListWorkspaces(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		log.Printf("failed to list managed containers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list workspaces"})
		return
	}

	// Cache environment display names to avoid redundant schema lookups.
	envNames := make(map[string]string)

	var workspaces []workspaceInfo
	for _, mc := range containers {
		ws := workspaceInfo{
			ID:        mc.ID,
			Name:      mc.Name,
			Status:    mc.Status,
			Subdomain: mc.Subdomain,
		}
		// Enrich with workspace-manager-level data from local DB.
		if rec, err := h.db.GetByContainerID(mc.ID); err == nil {
			ws.Environment = rec.EnvironmentID
			if name, ok := envNames[rec.EnvironmentID]; ok {
				ws.EnvironmentName = name
			} else if schema := h.fetchWorkspaceSchema(rec.EnvironmentID); schema != nil {
				ws.EnvironmentName = schema.DisplayName
				envNames[rec.EnvironmentID] = schema.DisplayName
			}
		}
		if mc.Subdomain != "" && h.baseDomain != "" {
			ws.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
		}
		workspaces = append(workspaces, ws)
	}

	if workspaces == nil {
		workspaces = []workspaceInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}

// CreateWorkspace launches a new workspace container.
func (h *Handler) CreateWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	var req struct {
		Name          string `json:"name" binding:"required"`
		EnvironmentID string `json:"environment_id" binding:"required"`
		DiskID        string `json:"disk_id,omitempty"`        // reuse existing disk (by stable ID)
		GitRepo       string `json:"git_repo,omitempty"`
		GitRef        string `json:"git_ref,omitempty"`
		PluginSource  string `json:"plugin_source,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	displayName := strings.TrimSpace(req.Name)
	if displayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	wsKey := slugify(displayName)
	if wsKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain at least one alphanumeric character"})
		return
	}

	// Look up the workspace environment schema (DB first, then live fallback).
	ws := h.fetchWorkspaceSchema(req.EnvironmentID)
	if ws == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown environment: " + req.EnvironmentID})
		return
	}

	// Generate an 8-char random ID for subdomain (permanent).
	var wsID string
	existingContainers, _ := h.sdk.ListManagedContainers()
	for attempts := 0; attempts < 10; attempts++ {
		candidate := randomID()
		subdCandidate := "ws-" + candidate
		collision := false
		for _, mc := range existingContainers {
			if mc.Subdomain == subdCandidate {
				collision = true
				break
			}
		}
		if !collision {
			wsID = candidate
			break
		}
	}
	if wsID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate unique workspace ID"})
		return
	}

	ctx := c.Request.Context()

	// Ensure all declared disks exist via storage-disk API, collect DiskMounts.
	var diskMounts []pluginsdk.DiskMount
	var workspaceDiskPath string
	for _, spec := range ws.Disks {
		var diskName string
		if spec.Type == "workspace" {
			if req.DiskID != "" {
				// Reusing an existing disk — verify it exists.
				data, err := h.sdk.RouteToPlugin(ctx, "storage-disk", "GET", fmt.Sprintf("/disks/by-id/%s", req.DiskID), nil)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "disk not found: " + req.DiskID})
					return
				}
				var existing struct {
					ID   string `json:"id"`
					Path string `json:"path"`
				}
				json.Unmarshal(data, &existing)
				diskMounts = append(diskMounts, pluginsdk.DiskMount{
					DiskID:   existing.ID,
					DiskType: "workspace",
					Target:   spec.Target,
				})
				workspaceDiskPath = strings.Replace(existing.Path, "/data/", "/", 1)
				continue
			}
			diskName = fmt.Sprintf("ws-%s-%s", wsID, wsKey)
		} else {
			diskName = spec.Name
		}

		diskID, diskPath, err := h.ensureDisk(ctx, diskName, spec.Type)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to ensure disk %s: %v", diskName, err)})
			return
		}
		diskMounts = append(diskMounts, pluginsdk.DiskMount{
			DiskID:   diskID,
			DiskType: spec.Type,
			Target:   spec.Target,
			ReadOnly: spec.ReadOnly,
		})
		if spec.Type == "workspace" {
			workspaceDiskPath = strings.Replace(diskPath, "/data/", "/", 1)
		}
	}

	// Git clone into workspace disk if requested (only for new disks).
	if req.GitRepo != "" && req.DiskID == "" && workspaceDiskPath != "" {
		cmd := exec.CommandContext(ctx, "git", "clone", req.GitRepo, ".")
		cmd.Dir = workspaceDiskPath
		if out, err := cmd.CombinedOutput(); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "git clone failed: " + string(out)})
			return
		}
		if req.GitRef != "" {
			checkout := exec.CommandContext(ctx, "git", "checkout", req.GitRef)
			checkout.Dir = workspaceDiskPath
			checkout.CombinedOutput()
		}
	}

	// Run workspace-type-specific setup scripts.
	for _, script := range ws.SetupScripts {
		switch script {
		case "code-server-navigator":
			if workspaceDiskPath != "" {
				setupCodeServerNavigator(workspaceDiskPath)
			}
		}
	}

	// Build env from workspace schema defaults.
	env := make(map[string]string)
	for k, v := range ws.EnvDefaults {
		env[k] = v
	}

	// Build cmd: base cmd + extra args from schema.
	cmd := ws.Cmd
	if len(ws.ExtraCmdArgs) > 0 {
		cmd = append(append([]string{}, cmd...), ws.ExtraCmdArgs...)
	}

	// Launch managed container via kernel.
	subdomain := "ws-" + wsID
	mc, err := h.sdk.CreateManagedContainer(pluginsdk.CreateManagedContainerRequest{
		Name:         displayName,
		Image:        ws.Image,
		Port:         ws.Port,
		Subdomain:    subdomain,
		DiskMounts:   diskMounts,
		Env:          env,
		Cmd:          cmd,
		DockerUser:   ws.DockerUser,
		PluginSource: req.PluginSource,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch workspace: " + err.Error()})
		return
	}

	h.db.Put(&storage.WorkspaceRecord{
		ContainerID:   mc.ID,
		EnvironmentID: req.EnvironmentID,
	})

	h.emitEvent("workspace:created", fmt.Sprintf(`{"id":"%s","environment":"%s","key":"%s"}`, mc.ID, req.EnvironmentID, wsKey))

	result := workspaceInfo{
		ID:        mc.ID,
		Name:      mc.Name,
		Status:    mc.Status,
		Subdomain: mc.Subdomain,
	}
	if mc.Subdomain != "" && h.baseDomain != "" {
		result.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	c.JSON(http.StatusCreated, result)
}

// GetWorkspace returns details for a single workspace.
func (h *Handler) GetWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	for _, mc := range containers {
		if mc.ID == id {
			ws := workspaceInfo{
				ID:        mc.ID,
				Name:      mc.Name,
				Status:    mc.Status,
				Subdomain: mc.Subdomain,
			}
			if mc.Subdomain != "" && h.baseDomain != "" {
				ws.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
			}
			c.JSON(http.StatusOK, ws)
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("workspace %q not found", id)})
}

// RenameWorkspace updates display name only.
// Disk stays linked by stable ID — no filesystem operations needed.
func (h *Handler) RenameWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	newDisplayName := strings.TrimSpace(req.Name)
	if newDisplayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	_, err := h.sdk.UpdateManagedContainer(id, pluginsdk.UpdateManagedContainerRequest{
		Name: &newDisplayName,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename workspace: " + err.Error()})
		return
	}

	h.emitEvent("workspace:renamed", fmt.Sprintf(`{"id":"%s","name":"%s"}`, id, newDisplayName))
	c.JSON(http.StatusOK, gin.H{"status": "renamed"})
}

// StartWorkspace re-launches a stopped workspace container with options applied.
func (h *Handler) StartWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	result, err := h.rebuildWorkspace(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.emitEvent("workspace:started", fmt.Sprintf(`{"id":"%s"}`, result.ID))
	c.JSON(http.StatusOK, result)
}

// StopWorkspace stops a running workspace container without deleting it.
func (h *Handler) StopWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	mc, err := h.sdk.StopManagedContainer(id)
	if err != nil {
		log.Printf("failed to stop workspace %s: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop workspace: " + err.Error()})
		return
	}

	h.emitEvent("workspace:stopped", fmt.Sprintf(`{"id":"%s"}`, id))
	c.JSON(http.StatusOK, workspaceInfo{
		ID:        mc.ID,
		Name:      mc.Name,
		Status:    mc.Status,
		Subdomain: mc.Subdomain,
	})
}

// RestartWorkspace recreates the workspace container with current options applied.
func (h *Handler) RestartWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	result, err := h.rebuildWorkspace(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.emitEvent("workspace:restarted", fmt.Sprintf(`{"id":"%s"}`, result.ID))
	c.JSON(http.StatusOK, result)
}

// rebuildWorkspace stops, deletes, and recreates a workspace container with
// the current environment schema + per-workspace options (env overrides, extra
// shared disks) applied. Used by both Start and Restart.
func (h *Handler) rebuildWorkspace(ctx context.Context, id string) (*workspaceInfo, error) {
	// Look up workspace record for environment ID.
	rec, err := h.db.GetByContainerID(id)
	if err != nil {
		return nil, fmt.Errorf("workspace not found")
	}

	// Get current container info (for subdomain, disk mounts).
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch workspaces")
	}
	var current *pluginsdk.ManagedContainerInfo
	for i := range containers {
		if containers[i].ID == id {
			current = &containers[i]
			break
		}
	}
	if current == nil {
		return nil, fmt.Errorf("workspace container not found")
	}

	// Fetch environment schema to rebuild creation params.
	ws := h.fetchWorkspaceSchema(rec.EnvironmentID)
	if ws == nil {
		return nil, fmt.Errorf("workspace environment schema not found")
	}

	// Build env from schema defaults.
	env := make(map[string]string)
	for k, v := range ws.EnvDefaults {
		env[k] = v
	}

	// Load per-workspace options: env overrides + extra shared disks.
	diskMounts := current.DiskMounts
	if opts, err := h.db.GetOptions(id); err == nil {
		if opts.EnvOverrides != "" {
			var overrides map[string]string
			if json.Unmarshal([]byte(opts.EnvOverrides), &overrides) == nil {
				for k, v := range overrides {
					env[k] = v
				}
			}
		}
		if opts.ExtraDisks != "" {
			var extras []storage.ExtraDisk
			if json.Unmarshal([]byte(opts.ExtraDisks), &extras) == nil {
				for _, d := range extras {
					// Resolve env vars like $HOME in the target path
					// using the workspace environment's defaults.
					target := os.Expand(d.Target, func(key string) string {
						if v, ok := env[key]; ok {
							return v
						}
						return "$" + key
					})
					diskMounts = append(diskMounts, pluginsdk.DiskMount{
						DiskID:   d.DiskID,
						DiskType: "shared",
						Target:   target,
						ReadOnly: d.ReadOnly,
					})
				}
			}
		}
	}

	// Build cmd.
	cmd := ws.Cmd
	if len(ws.ExtraCmdArgs) > 0 {
		cmd = append(append([]string{}, cmd...), ws.ExtraCmdArgs...)
	}

	// Stop and delete old container.
	h.sdk.StopManagedContainer(id)
	h.sdk.DeleteManagedContainer(id)

	// Recreate with same subdomain + env schema disks + extra shared disks.
	mc, err := h.sdk.CreateManagedContainer(pluginsdk.CreateManagedContainerRequest{
		Name:       current.Name,
		Image:      ws.Image,
		Port:       ws.Port,
		Subdomain:  current.Subdomain,
		DiskMounts: diskMounts,
		Env:        env,
		Cmd:        cmd,
		DockerUser: ws.DockerUser,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to recreate workspace: %w", err)
	}

	// Update workspace record with new container ID.
	h.db.Delete(id)
	h.db.Put(&storage.WorkspaceRecord{
		ContainerID:   mc.ID,
		EnvironmentID: rec.EnvironmentID,
	})

	// Migrate options to new container ID and re-attach sidecar if needed.
	if opts, err := h.db.GetOptions(id); err == nil {
		opts.ContainerID = mc.ID

		// Re-attach agent sidecar if workspace has an agent configured.
		if opts.AgentPlugin != "" {
			// Detach old sidecar first (cleans up stale alias/persona/plugin).
			if opts.SidecarID != "" {
				h.detachSidecar(ctx, current.Subdomain, opts.SidecarID)
				opts.SidecarID = ""
			}
			sidecarID, err := h.attachSidecar(ctx, mc.ID, current.Subdomain, opts.AgentPlugin, opts.AgentModel, diskMounts)
			if err != nil {
				log.Printf("rebuildWorkspace: failed to re-attach sidecar: %v", err)
			} else {
				opts.SidecarID = sidecarID
			}
		}

		h.db.PutOptions(opts)
		h.db.DeleteOptions(id)
	}

	result := &workspaceInfo{
		ID:          mc.ID,
		Name:        mc.Name,
		Environment: rec.EnvironmentID,
		Status:      mc.Status,
		Subdomain:   mc.Subdomain,
	}
	if mc.Subdomain != "" && h.baseDomain != "" {
		result.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	return result, nil
}

// DeleteWorkspace stops the container, cleans up any sidecar, and removes the workspace record.
// The disk data is never deleted — use storage-disk for disk management.
func (h *Handler) DeleteWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	// Clean up sidecar if attached.
	if opts, err := h.db.GetOptions(id); err == nil && opts.SidecarID != "" {
		// Need subdomain for alias cleanup.
		containers, _ := h.sdk.ListManagedContainers()
		for _, mc := range containers {
			if mc.ID == id {
				h.detachSidecar(c.Request.Context(), mc.Subdomain, opts.SidecarID)
				break
			}
		}
	}

	if err := h.sdk.DeleteManagedContainer(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete workspace: " + err.Error()})
		return
	}

	h.db.DeleteOptions(id)
	h.db.Delete(id)
	h.emitEvent("workspace:deleted", fmt.Sprintf(`{"id":"%s"}`, id))
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

// --- Git persistence ---

func (h *Handler) PersistWorkspace(c *gin.Context) {
	id := c.Param("id")

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var wsPath string
	for _, mc := range containers {
		if mc.ID == id {
			resolved, err := h.resolveWorkspaceDiskPath(c.Request.Context(), mc.DiskMounts)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve workspace disk: " + err.Error()})
				return
			}
			wsPath = resolved
			break
		}
	}
	if wsPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("workspace %q not found", id)})
		return
	}

	var req struct {
		CommitMessage string `json:"commit_message,omitempty"`
		Push          bool   `json:"push"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx := c.Request.Context()

	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = wsPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "git add failed: " + string(out)})
		return
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	diffCmd.Dir = wsPath
	if diffCmd.Run() == nil {
		c.JSON(http.StatusOK, gin.H{"message": "nothing to commit", "pushed": false})
		return
	}

	msg := req.CommitMessage
	if msg == "" {
		msg = "workspace changes via workspace-manager"
	}
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", msg)
	commitCmd.Dir = wsPath
	commitOut, err := commitCmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "git commit failed: " + string(commitOut)})
		return
	}

	h.emitEvent("workspace:persisted", fmt.Sprintf(`{"id":"%s","pushed":%v}`, id, req.Push))

	pushed := false
	if req.Push {
		pushCmd := exec.CommandContext(ctx, "git", "push")
		pushCmd.Dir = wsPath
		pushOut, err := pushCmd.CombinedOutput()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"committed": true,
				"pushed":    false,
				"error":     "git push failed: " + string(pushOut),
			})
			return
		}
		pushed = true
	}

	c.JSON(http.StatusOK, gin.H{
		"committed": true,
		"pushed":    pushed,
	})
}

// --- helpers ---

// HandleEnvironmentRegister processes a workspace:environment:register event
// and upserts the environment into the local DB.
func (h *Handler) HandleEnvironmentRegister(detail string) {
	var payload events.WorkspaceEnvironmentRegisterPayload
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		log.Printf("handleEnvironmentRegister: bad payload: %v", err)
		return
	}
	if payload.PluginID == "" || payload.Image == "" || payload.Port == 0 {
		log.Printf("handleEnvironmentRegister: incomplete payload from %s", payload.PluginID)
		return
	}

	cmdJSON, _ := json.Marshal(payload.Cmd)
	extraCmdJSON, _ := json.Marshal(payload.ExtraCmdArgs)
	disksJSON, _ := json.Marshal(payload.Disks)
	envJSON, _ := json.Marshal(payload.EnvDefaults)

	rec := &storage.EnvironmentRecord{
		PluginID:     payload.PluginID,
		DisplayName:  payload.DisplayName,
		Description:  payload.Description,
		Image:        payload.Image,
		Port:         payload.Port,
		Icon:         payload.Icon,
		DockerUser:   payload.DockerUser,
		Cmd:          string(cmdJSON),
		ExtraCmdArgs: string(extraCmdJSON),
		Disks:        string(disksJSON),
		EnvDefaults:  string(envJSON),
		Status:       "healthy",
	}

	if err := h.db.UpsertEnvironment(rec); err != nil {
		log.Printf("handleEnvironmentRegister: upsert %s: %v", payload.PluginID, err)
		return
	}
	log.Printf("registered workspace environment: %s (%s)", payload.DisplayName, payload.PluginID)
}

// fetchWorkspaceSchema returns workspace schema data for a given environment plugin.
func (h *Handler) fetchWorkspaceSchema(pluginID string) *workspaceSchemaData {
	// Try DB first (push-based registration).
	if rec, err := h.db.GetEnvironment(pluginID); err == nil {
		ws := &workspaceSchemaData{
			DisplayName: rec.DisplayName,
			Description: rec.Description,
			Image:       rec.Image,
			Port:        rec.Port,
			Icon:        rec.Icon,
			DockerUser:  rec.DockerUser,
		}
		json.Unmarshal([]byte(rec.Cmd), &ws.Cmd)
		json.Unmarshal([]byte(rec.ExtraCmdArgs), &ws.ExtraCmdArgs)
		json.Unmarshal([]byte(rec.Disks), &ws.Disks)
		json.Unmarshal([]byte(rec.EnvDefaults), &ws.EnvDefaults)
		if ws.Image != "" && ws.Port != 0 {
			return ws
		}
	}

	// Fallback: live schema fetch via P2P/kernel proxy.
	if h.sdk == nil {
		return nil
	}
	schema, err := h.sdk.GetPluginSchema(pluginID)
	if err != nil {
		log.Printf("fetchWorkspaceSchema(%s): %v", pluginID, err)
		return nil
	}

	wsRaw, ok := schema["workspace"]
	if !ok {
		return nil
	}

	b, err := json.Marshal(wsRaw)
	if err != nil {
		return nil
	}
	var ws workspaceSchemaData
	if err := json.Unmarshal(b, &ws); err != nil {
		return nil
	}
	if ws.Image == "" || ws.Port == 0 {
		return nil
	}
	return &ws
}

type workspaceSchemaData struct {
	DisplayName  string                  `json:"display_name"`
	Description  string                  `json:"description"`
	Image        string                  `json:"image"`
	Port         int                     `json:"port"`
	Icon         string                  `json:"icon"`
	Cmd          []string                `json:"cmd"`
	DockerUser   string                  `json:"docker_user"`
	EnvDefaults  map[string]string       `json:"env_defaults"`
	Disks        []events.WorkspaceDiskSpec `json:"disks"`
	ExtraCmdArgs []string                `json:"extra_cmd_args"`
	SetupScripts []string                `json:"setup_scripts"`
}

// GetWorkspaceOptions returns per-workspace overrides.
func (h *Handler) GetWorkspaceOptions(c *gin.Context) {
	id := c.Param("id")

	opts, err := h.db.GetOptions(id)
	if err != nil {
		// No options stored yet — return empty defaults.
		c.JSON(http.StatusOK, storage.WorkspaceOptions{ContainerID: id})
		return
	}
	c.JSON(http.StatusOK, opts)
}

// UpdateWorkspaceOptions saves per-workspace overrides.
func (h *Handler) UpdateWorkspaceOptions(c *gin.Context) {
	id := c.Param("id")

	// Verify workspace exists.
	if _, err := h.db.GetByContainerID(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	var req struct {
		EnvOverrides map[string]string     `json:"env_overrides"`
		ExtraDisks   []storage.ExtraDisk   `json:"extra_disks"`
		AgentPlugin  string                `json:"agent_plugin"`
		AgentModel   string                `json:"agent_model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate extra disks.
	for _, d := range req.ExtraDisks {
		if d.DiskID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "extra disk must have a disk_id"})
			return
		}
		if d.Target == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "extra disk must have a target path"})
			return
		}
	}

	// Serialize JSON fields.
	envJSON, _ := json.Marshal(req.EnvOverrides)
	disksJSON, _ := json.Marshal(req.ExtraDisks)

	opts := &storage.WorkspaceOptions{
		ContainerID:  id,
		EnvOverrides: string(envJSON),
		ExtraDisks:   string(disksJSON),
		AgentPlugin:  req.AgentPlugin,
		AgentModel:   req.AgentModel,
	}

	// Check if agent plugin changed — manage sidecar lifecycle.
	var oldAgentPlugin, oldSidecarID string
	if existing, err := h.db.GetOptions(id); err == nil {
		oldAgentPlugin = existing.AgentPlugin
		oldSidecarID = existing.SidecarID
		opts.SidecarID = oldSidecarID
	}

	// If agent plugin changed, detach old sidecar and attach new one.
	if req.AgentPlugin != oldAgentPlugin {
		// Get workspace container info for subdomain and disk mounts.
		containers, _ := h.sdk.ListManagedContainers()
		var subdomain string
		var diskMounts []pluginsdk.DiskMount
		for _, mc := range containers {
			if mc.ID == id {
				subdomain = mc.Subdomain
				diskMounts = mc.DiskMounts
				break
			}
		}

		// Detach old sidecar if present.
		if oldSidecarID != "" && subdomain != "" {
			h.detachSidecar(c.Request.Context(), subdomain, oldSidecarID)
			opts.SidecarID = ""
		}

		// Attach new sidecar if requested.
		if req.AgentPlugin != "" && subdomain != "" {
			sidecarID, err := h.attachSidecar(c.Request.Context(), id, subdomain, req.AgentPlugin, req.AgentModel, diskMounts)
			if err != nil {
				log.Printf("sidecar attach failed: %v", err)
			} else {
				opts.SidecarID = sidecarID
			}
		}
	}

	if err := h.db.PutOptions(opts); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save options: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, opts)
}

// setupCodeServerNavigator provisions Machine settings for navigator support.
func setupCodeServerNavigator(workspaceDiskPath string) {
	// Inline the setup since it's a one-time operation.
	exec.Command("mkdir", "-p", workspaceDiskPath+"/.code-server/code-server/Machine").Run()
	exec.Command("sh", "-c", fmt.Sprintf(`echo '{"extensions.supportNodeGlobalNavigator":true}' > '%s/.code-server/code-server/Machine/settings.json'`, workspaceDiskPath)).Run()
}
