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
	"github.com/antimatter-studios/teamagentica/plugins/infra-workspace-manager/internal/storage"
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

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"plugin":  "infra-workspace-manager",
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

// ListEnvironments returns all installed workspace plugins.
func (h *Handler) ListEnvironments(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	plugins, err := h.sdk.SearchPlugins("workspace:environment")
	if err != nil {
		log.Printf("failed to search workspace plugins: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to discover environments"})
		return
	}

	var envs []environmentInfo
	for _, p := range plugins {
		ws := h.fetchWorkspaceSchema(p.ID)
		if ws == nil {
			continue
		}
		envs = append(envs, environmentInfo{
			PluginID:    p.ID,
			Name:        ws.DisplayName,
			Description: ws.Description,
			Image:       ws.Image,
			Port:        ws.Port,
			Icon:        ws.Icon,
		})
	}

	if envs == nil {
		envs = []environmentInfo{}
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
	VolumeName      string `json:"volume_name"`
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
			VolumeName: mc.VolumeName,
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
		VolumeName    string `json:"volume_name,omitempty"` // reuse existing volume
		GitRepo       string `json:"git_repo,omitempty"`
		GitRef        string `json:"git_ref,omitempty"`
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

	// Look up the workspace plugin to get its schema.
	plugins, err := h.sdk.SearchPlugins("workspace:environment")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to discover environments"})
		return
	}

	var ws *workspaceSchemaData
	found := false
	for _, p := range plugins {
		if p.ID == req.EnvironmentID {
			ws = h.fetchWorkspaceSchema(p.ID)
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown environment: " + req.EnvironmentID})
		return
	}
	if ws == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown environment: " + req.EnvironmentID})
		return
	}

	// Generate an 8-char random ID for subdomain (permanent) and volume prefix.
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

	// Reuse existing volume or create a new one.
	var volumeName string
	var volumeExisted bool
	if req.VolumeName != "" {
		volumeName = req.VolumeName
		volumePath := filepath.Join(h.workspaceDir, "volumes", volumeName)
		if info, err := os.Stat(volumePath); err == nil && info.IsDir() {
			volumeExisted = true
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "volume not found: " + req.VolumeName})
			return
		}
	} else {
		volumeName = fmt.Sprintf("ws-%s-%s", wsID, wsKey)
		volumeExisted = false
	}
	volumePath := filepath.Join(h.workspaceDir, "volumes", volumeName)
	if !volumeExisted {
		if err := os.MkdirAll(volumePath, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create volume: " + err.Error()})
			return
		}
	}

	// Git clone into volume if requested (skip if reusing existing volume).
	if req.GitRepo != "" && !volumeExisted {
		cmd := exec.CommandContext(c.Request.Context(), "git", "clone", req.GitRepo, ".")
		cmd.Dir = volumePath
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(volumePath)
			c.JSON(http.StatusBadGateway, gin.H{"error": "git clone failed: " + string(out)})
			return
		}
		if req.GitRef != "" {
			checkout := exec.CommandContext(c.Request.Context(), "git", "checkout", req.GitRef)
			checkout.Dir = volumePath
			checkout.CombinedOutput()
		}
	}

	// Ensure shared mount directories exist on the host volume.
	for _, sm := range ws.SharedMounts {
		if sm.VolumeName != "" {
			os.MkdirAll(filepath.Join(h.workspaceDir, "volumes", sm.VolumeName), 0755)
		}
	}

	// Run workspace-type-specific setup scripts declared in the schema.
	for _, script := range ws.SetupScripts {
		switch script {
		case "code-server-navigator":
			// Provision Machine settings per-workspace for navigator support.
			csSettingsDir := filepath.Join(volumePath, ".code-server", "code-server", "Machine")
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
		Name:        displayName,
		Image:       ws.Image,
		Port:        ws.Port,
		Subdomain:   subdomain,
		VolumeName:  volumeName,
		ExtraMounts: ws.SharedMounts,
		Env:         env,
		Cmd:         cmd,
		DockerUser:  ws.DockerUser,
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
		VolumeName: mc.VolumeName,
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
				VolumeName: mc.VolumeName,
			}
			if mc.Subdomain != "" && h.baseDomain != "" {
				ws.URL = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
			}
			c.JSON(http.StatusOK, ws)
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
}

// RenameWorkspace updates display name and volume directory slug.
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

	// Find the workspace to get current volume name.
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
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	// Extract the random ID prefix from the current volume name (ws-{id}-{slug}).
	// Volume format: "ws-{6hex}-{slug}"
	wsPrefix := extractVolumePrefix(found.VolumeName)
	newVolumeName := wsPrefix + newSlug

	// Only rename volume dir if the slug actually changed.
	if newVolumeName != found.VolumeName {
		oldPath := filepath.Join(h.workspaceDir, "volumes", found.VolumeName)
		newPath := filepath.Join(h.workspaceDir, "volumes", newVolumeName)

		// Check no volume with new name exists.
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

		_, err = h.sdk.UpdateManagedContainer(id, pluginsdk.UpdateManagedContainerRequest{
			Name:       &newDisplayName,
			VolumeName: &newVolumeName,
		})
		if err != nil {
			// Rollback volume rename.
			os.Rename(
				filepath.Join(h.workspaceDir, "volumes", newVolumeName),
				filepath.Join(h.workspaceDir, "volumes", found.VolumeName),
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

// extractVolumePrefix returns the "ws-{id}-" prefix from a volume name like "ws-a1b2c3d4-my-project".
// Falls back to returning the full name + "-" if format doesn't match.
func extractVolumePrefix(volumeName string) string {
	// Expected format: ws-{8hex}-{slug}
	if strings.HasPrefix(volumeName, "ws-") && len(volumeName) > 12 {
		// "ws-" (3) + 8 hex chars + "-" = index 12
		if volumeName[11] == '-' {
			return volumeName[:12] // "ws-a1b2c3d4-"
		}
	}
	// Legacy format without random ID — use whole name as prefix.
	return volumeName + "-"
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

// DeleteWorkspace stops the container and optionally removes the volume.
func (h *Handler) DeleteWorkspace(c *gin.Context) {
	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sdk not ready"})
		return
	}

	id := c.Param("id")
	removeVolume := c.Query("remove_volume") == "true"

	// Find the container to get volume name before deleting.
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var volumeName string
	for _, mc := range containers {
		if mc.ID == id {
			volumeName = mc.VolumeName
			break
		}
	}

	if err := h.sdk.DeleteManagedContainer(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop workspace: " + err.Error()})
		return
	}

	// Clean up local workspace record.
	h.db.Delete(id)

	if removeVolume && volumeName != "" {
		volumePath := filepath.Join(h.workspaceDir, "volumes", volumeName)
		if err := os.RemoveAll(volumePath); err != nil {
			log.Printf("warning: failed to remove volume %s: %v", volumePath, err)
		}
	}

	h.emitEvent("workspace:deleted", fmt.Sprintf(`{"id":"%s"}`, id))
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

// --- Git persistence (operates on the volume directly) ---

func (h *Handler) PersistWorkspace(c *gin.Context) {
	id := c.Param("id")

	// Find the workspace to get volume name.
	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch workspaces"})
		return
	}

	var volumeName string
	for _, mc := range containers {
		if mc.ID == id {
			volumeName = mc.VolumeName
			break
		}
	}
	if volumeName == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	wsPath := filepath.Join(h.workspaceDir, "volumes", volumeName)
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

// fetchWorkspaceSchema queries the plugin's live schema via the kernel proxy
// and extracts the "workspace" section.
func (h *Handler) fetchWorkspaceSchema(pluginID string) *workspaceSchemaData {
	schema, err := h.sdk.GetPluginSchema(pluginID)
	if err != nil {
		log.Printf("fetchWorkspaceSchema(%s): %v", pluginID, err)
		return nil
	}

	wsRaw, ok := schema["workspace"]
	if !ok {
		return nil
	}

	// Re-marshal and unmarshal through JSON to convert map[string]interface{} properly.
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

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// volumeExists checks if a volume directory exists under the workspace dir.
func (h *Handler) volumeExists(volumeName string) bool {
	_, err := os.Stat(filepath.Join(h.workspaceDir, "volumes", volumeName))
	return err == nil
}

// ListVolumes returns volume directories with status flags.
// Each volume includes whether it has an active workspace and whether it's empty.
func (h *Handler) ListVolumes(c *gin.Context) {
	volumesDir := filepath.Join(h.workspaceDir, "volumes")
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"volumes": []gin.H{}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Build set of volume names with active workspaces.
	activeVolumes := make(map[string]bool)
	if h.sdk != nil {
		if containers, err := h.sdk.ListManagedContainers(); err == nil {
			for _, mc := range containers {
				if mc.VolumeName != "" {
					activeVolumes[mc.VolumeName] = true
				}
			}
		}
	}

	var volumes []gin.H
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		volPath := filepath.Join(volumesDir, e.Name())
		size := dirSize(volPath)
		info, _ := e.Info()

		tagInfo := DetectVolumeTags(volPath)

		vol := gin.H{
			"name":          e.Name(),
			"size_bytes":    size,
			"is_empty":      size == 0,
			"has_workspace": activeVolumes[e.Name()],
			"tags":          tagInfo.Tags,
			"git_remote":    tagInfo.GitRemote,
			"extensions":    tagInfo.Extensions,
		}
		if tagInfo.Tags == nil {
			vol["tags"] = []string{}
		}
		if tagInfo.Extensions == nil {
			vol["extensions"] = []string{}
		}
		if info != nil {
			vol["created_at"] = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		volumes = append(volumes, vol)
	}

	if volumes == nil {
		volumes = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

// DeleteVolume removes a volume directory if it has no active workspace.
func (h *Handler) DeleteVolume(c *gin.Context) {
	name := c.Param("name")

	if !isValidWorkspaceID(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid volume name"})
		return
	}

	// Check no active workspace is using this volume.
	if h.sdk != nil {
		if containers, err := h.sdk.ListManagedContainers(); err == nil {
			for _, mc := range containers {
				if mc.VolumeName == name {
					c.JSON(http.StatusConflict, gin.H{"error": "volume has an active workspace"})
					return
				}
			}
		}
	}

	volumePath := filepath.Join(h.workspaceDir, "volumes", name)
	if _, err := os.Stat(volumePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "volume not found"})
		return
	}

	if err := os.RemoveAll(volumePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete volume: " + err.Error()})
		return
	}

	h.emitEvent("volume:deleted", fmt.Sprintf(`{"name":"%s"}`, name))
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "name": name})
}
