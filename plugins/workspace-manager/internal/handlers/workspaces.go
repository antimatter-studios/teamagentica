package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	workspaceDir string
	baseDomain   string
	debug        bool
	sdk          *pluginsdk.Client
	db           *storage.DB
}

func NewHandler(workspaceDir, baseDomain string, debug bool, db *storage.DB) *Handler {
	return &Handler{
		workspaceDir: workspaceDir,
		baseDomain:   baseDomain,
		debug:        debug,
		db:           db,
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// diskPath returns the absolute path for a workspace disk (or the workspace disks root if name is empty).
func (h *Handler) diskPath(name ...string) string {
	if len(name) == 0 || name[0] == "" {
		return filepath.Join(h.workspaceDir, "workspace")
	}
	return filepath.Join(h.workspaceDir, "workspace", name[0])
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.PublishEvent(eventType, detail)
	}
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
	DiskName      string `json:"disk_name"`
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
			ID:         mc.ID,
			Name:       mc.Name,
			Status:     mc.Status,
			Subdomain:  mc.Subdomain,
			DiskName: mc.DiskName,
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
		DiskName    string `json:"disk_name,omitempty"`      // reuse existing disk
		GitRepo       string `json:"git_repo,omitempty"`
		GitRef        string `json:"git_ref,omitempty"`
		PluginSource  string `json:"plugin_source,omitempty"` // plugin name whose source to bind-mount for dev editing
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

	// Generate an 8-char random ID for subdomain (permanent) and disk prefix.
	// Check for collisions against existing workspaces.
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

	// Reuse existing disk dir or create a new one.
	var diskName string
	var diskExisted bool
	if req.DiskName != "" {
		diskName = req.DiskName
		dskPath := h.diskPath(diskName)
		if info, err := os.Stat(dskPath); err == nil && info.IsDir() {
			diskExisted = true
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "disk not found: " + req.DiskName})
			return
		}
	} else {
		diskName = fmt.Sprintf("ws-%s-%s", wsID, wsKey)
		diskExisted = false
	}
	dskPath := h.diskPath(diskName)
	if !diskExisted {
		if err := os.MkdirAll(dskPath, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create disk: " + err.Error()})
			return
		}
	}

	// Git clone into disk if requested (skip if reusing existing disk).
	if req.GitRepo != "" && !diskExisted {
		cmd := exec.CommandContext(c.Request.Context(), "git", "clone", req.GitRepo, ".")
		cmd.Dir = dskPath
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(dskPath)
			c.JSON(http.StatusBadGateway, gin.H{"error": "git clone failed: " + string(out)})
			return
		}
		if req.GitRef != "" {
			checkout := exec.CommandContext(c.Request.Context(), "git", "checkout", req.GitRef)
			checkout.Dir = dskPath
			checkout.CombinedOutput()
		}
	}

	// Ensure shared mount directories exist on the host disk.
	for _, sm := range ws.SharedMounts {
		if sm.DiskName != "" {
			os.MkdirAll(h.diskPath(sm.DiskName), 0755)
		}
	}

	// Run workspace-type-specific setup scripts declared in the schema.
	for _, script := range ws.SetupScripts {
		switch script {
		case "code-server-navigator":
			// Provision Machine settings per-workspace for navigator support.
			csSettingsDir := filepath.Join(dskPath, ".code-server", "code-server", "Machine")
			if err := os.MkdirAll(csSettingsDir, 0755); err == nil {
				_ = os.WriteFile(filepath.Join(csSettingsDir, "settings.json"),
					[]byte(`{"extensions.supportNodeGlobalNavigator":true}`+"\n"), 0644)
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
	// Subdomain uses only the random ID — permanent, never changes on rename.
	subdomain := "ws-" + wsID
	mc, err := h.sdk.CreateManagedContainer(pluginsdk.CreateManagedContainerRequest{
		Name:         displayName,
		Image:        ws.Image,
		Port:         ws.Port,
		Subdomain:    subdomain,
		DiskName:   diskName,
		ExtraMounts:  ws.SharedMounts,
		Env:          env,
		Cmd:          cmd,
		DockerUser:   ws.DockerUser,
		PluginSource: req.PluginSource,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch workspace: " + err.Error()})
		return
	}

	// Track workspace-level metadata in local DB.
	h.db.Put(&storage.WorkspaceRecord{
		ContainerID:   mc.ID,
		EnvironmentID: req.EnvironmentID,
	})

	h.emitEvent("workspace:created", fmt.Sprintf(`{"id":"%s","environment":"%s","key":"%s"}`, mc.ID, req.EnvironmentID, wsKey))

	result := workspaceInfo{
		ID:         mc.ID,
		Name:       mc.Name,
		Status:     mc.Status,
		Subdomain:  mc.Subdomain,
		DiskName: mc.DiskName,
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
				ID:         mc.ID,
				Name:       mc.Name,
				Status:     mc.Status,
				Subdomain:  mc.Subdomain,
				DiskName: mc.DiskName,
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

// RenameWorkspace updates display name and disk directory slug.
// Subdomain is permanent (based on random ID) — never changes.
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
	newSlug := slugify(newDisplayName)
	if newSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain at least one alphanumeric character"})
		return
	}

	// Find the workspace to get current disk name.
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var found *pluginsdk.ManagedContainerInfo
	for _, mc := range containers {
		if mc.ID == id {
			found = &mc
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("workspace %q not found", id)})
		return
	}

	// Extract the random ID prefix from the current disk name (ws-{id}-{slug}).
	// Disk name format: "ws-{6hex}-{slug}"
	wsPrefix := extractDiskPrefix(found.DiskName)
	newDiskName := wsPrefix + newSlug

	// Only rename disk dir if the slug actually changed.
	if newDiskName != found.DiskName {
		oldPath := h.diskPath(found.DiskName)
		newPath := h.diskPath(newDiskName)

		// Check no disk with new name exists.
		if _, err := os.Stat(newPath); err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "a disk with that name already exists"})
			return
		}

		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename disk: " + err.Error()})
				return
			}
		}

		_, err = h.sdk.UpdateManagedContainer(id, pluginsdk.UpdateManagedContainerRequest{
			Name:       &newDisplayName,
			DiskName: &newDiskName,
		})
		if err != nil {
			// Rollback disk rename.
			os.Rename(
				h.diskPath(newDiskName),
				h.diskPath(found.DiskName),
			)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workspace: " + err.Error()})
			return
		}
	} else {
		// Slug unchanged, just update display name.
		_, err = h.sdk.UpdateManagedContainer(id, pluginsdk.UpdateManagedContainerRequest{
			Name: &newDisplayName,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename workspace: " + err.Error()})
			return
		}
	}

	h.emitEvent("workspace:renamed", fmt.Sprintf(`{"id":"%s","name":"%s"}`, id, newDisplayName))
	c.JSON(http.StatusOK, gin.H{"status": "renamed"})
}

// extractDiskPrefix returns the "ws-{id}-" prefix from a disk name like "ws-a1b2c3d4-my-project".
// Falls back to returning the full name + "-" if format doesn't match.
func extractDiskPrefix(diskName string) string {
	// Expected format: ws-{8hex}-{slug}
	if strings.HasPrefix(diskName, "ws-") && len(diskName) > 12 {
		// "ws-" (3) + 8 hex chars + "-" = index 12
		if diskName[11] == '-' {
			return diskName[:12] // "ws-a1b2c3d4-"
		}
	}
	// Legacy format without random ID — use whole name as prefix.
	return diskName + "-"
}

// StartWorkspace re-launches a stopped workspace container.
func (h *Handler) StartWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")
	mc, err := h.sdk.StartManagedContainer(id)
	if err != nil {
		log.Printf("failed to start workspace %s: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start workspace: " + err.Error()})
		return
	}

	h.emitEvent("workspace:started", fmt.Sprintf(`{"id":"%s"}`, id))

	result := workspaceInfo{
		ID:         mc.ID,
		Name:       mc.Name,
		Status:     mc.Status,
		Subdomain:  mc.Subdomain,
		DiskName: mc.DiskName,
	}
	if rec, err := h.db.GetByContainerID(mc.ID); err == nil {
		result.Environment = rec.EnvironmentID
	}
	if mc.Subdomain != "" && h.baseDomain != "" {
		result.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	c.JSON(http.StatusOK, result)
}

// DeleteWorkspace stops the container and removes the workspace record.
// The disk data is never deleted — use storage-disk for disk management.
func (h *Handler) DeleteWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")

	if err := h.sdk.DeleteManagedContainer(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete workspace: " + err.Error()})
		return
	}

	h.db.Delete(id)
	h.emitEvent("workspace:deleted", fmt.Sprintf(`{"id":"%s"}`, id))
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

// --- Git persistence (operates on the disk directly) ---

func (h *Handler) PersistWorkspace(c *gin.Context) {
	id := c.Param("id")

	// Find the workspace to get disk name.
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var diskName string
	for _, mc := range containers {
		if mc.ID == id {
			diskName = mc.DiskName
			break
		}
	}
	if diskName == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("workspace %q not found", id)})
		return
	}

	wsPath := h.diskPath(diskName)
	if _, err := os.Stat(filepath.Join(wsPath, ".git")); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace is not a git repository"})
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

	// Serialize slice/map fields to JSON for DB storage.
	cmdJSON, _ := json.Marshal(payload.Cmd)
	extraCmdJSON, _ := json.Marshal(payload.ExtraCmdArgs)
	mountsJSON, _ := json.Marshal(payload.SharedMounts)
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
		SharedMounts: string(mountsJSON),
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
// Reads from local DB first (populated via push-based registration).
// Falls back to live schema fetch for backwards compatibility with unregistered plugins.
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
		json.Unmarshal([]byte(rec.SharedMounts), &ws.SharedMounts)
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
	DisplayName  string                 `json:"display_name"`
	Description  string                 `json:"description"`
	Image        string                 `json:"image"`
	Port         int                    `json:"port"`
	Icon         string                 `json:"icon"`
	Cmd          []string               `json:"cmd"`
	DockerUser   string                 `json:"docker_user"`
	EnvDefaults  map[string]string      `json:"env_defaults"`
	SharedMounts []pluginsdk.ExtraMount `json:"shared_mounts"`
	ExtraCmdArgs []string               `json:"extra_cmd_args"`
	SetupScripts []string               `json:"setup_scripts"` // e.g. ["code-server-navigator"]
}

