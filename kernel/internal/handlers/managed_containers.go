package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// waitForReady polls a managed container's HTTP port until it responds or the
// timeout expires. This prevents the frontend from redirecting to a workspace
// URL before the container's application is actually serving.
func waitForReady(mc *models.ManagedContainer, timeout time.Duration) {
	target := fmt.Sprintf("http://teamagentica-mc-%s:%d/", mc.ID, mc.Port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(target)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("managed container %s: readiness timeout after %s", mc.ID, timeout)
}

// --- response types ---

// managedContainerResponse is the API response for a managed container.
// The ManagedContainer model uses json:"-" for internal fields, so we
// build a response that includes disk mounts explicitly.
type managedContainerResponse struct {
	ID           string             `json:"id"`
	PluginID     string             `json:"plugin_id"`
	Name         string             `json:"name"`
	Image        string             `json:"image"`
	Status       string             `json:"status"`
	Port         int                `json:"port"`
	Subdomain    string             `json:"subdomain"`
	PluginSource string             `json:"plugin_source,omitempty"`
	DiskMounts   []models.DiskMount `json:"disk_mounts,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

func mcResponse(mc *models.ManagedContainer) managedContainerResponse {
	return managedContainerResponse{
		ID:           mc.ID,
		PluginID:     mc.PluginID,
		Name:         mc.Name,
		Image:        mc.Image,
		Status:       mc.Status,
		Port:         mc.Port,
		Subdomain:    mc.Subdomain,
		PluginSource: mc.PluginSource,
		DiskMounts:   mc.GetDiskMounts(),
		CreatedAt:    mc.CreatedAt,
		UpdatedAt:    mc.UpdatedAt,
	}
}

// --- request types ---

type createManagedContainerRequest struct {
	Name         string              `json:"name" binding:"required"`
	Image        string              `json:"image" binding:"required"`
	Port         int                 `json:"port" binding:"required"`
	Subdomain    string              `json:"subdomain" binding:"required"`
	DiskMounts   []models.DiskMount  `json:"disk_mounts"`
	Env          map[string]string   `json:"env"`
	Cmd          []string            `json:"cmd"`
	DockerUser   string              `json:"docker_user"`
	PluginSource string              `json:"plugin_source"`
}

// --- helpers ---

// extractPluginID returns the plugin ID from the mTLS-authenticated context.
// PluginTokenAuth middleware sets "plugin_id" from the client certificate CN.
func extractPluginID(c *gin.Context) (string, bool) {
	if pluginID, exists := c.Get("plugin_id"); exists {
		if id, ok := pluginID.(string); ok && id != "" {
			return id, true
		}
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "mTLS client certificate required"})
	return "", false
}

// generateContainerID returns a 32-char cryptographically random hex string.
// This makes container IDs unguessable (128 bits of entropy), preventing
// brute-force access to the unauthenticated /ws/:container_id proxy.
func generateContainerID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// resolveDiskPaths resolves DiskMount entries to host-side paths via storage-disk's API.
// Each DiskMount.DiskID is looked up with GET /disks/by-id/:id → current path,
// then translated to a host-side bind mount path.
func (h *PluginHandler) resolveManagedDiskPaths(ctx context.Context, mounts []models.DiskMount) ([]runtime.ResolvedDiskMount, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	// Find storage-disk plugin address.
	var storageDisk models.Plugin
	if err := h.db().First(&storageDisk, "id = ?", "storage-disk").Error; err != nil {
		return nil, fmt.Errorf("storage-disk plugin not found: %w", err)
	}
	if storageDisk.Host == "" || storageDisk.HTTPPort == 0 {
		return nil, fmt.Errorf("storage-disk plugin not ready (host=%q port=%d)", storageDisk.Host, storageDisk.HTTPPort)
	}

	scheme := "http"
	if h.clientTLS != nil {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, storageDisk.Host, storageDisk.HTTPPort)
	client := &http.Client{Timeout: 10 * time.Second, Transport: h.transport}

	var resolved []runtime.ResolvedDiskMount
	for _, dm := range mounts {
		if dm.DiskID == "" || dm.Target == "" {
			continue
		}

		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/disks/by-id/%s", baseURL, dm.DiskID), nil)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve disk %s: %w", dm.DiskID, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("storage-disk returned %d for disk %s: %s", resp.StatusCode, dm.DiskID, string(body))
		}

		var result struct {
			Path string `json:"path"`
		}
		json.NewDecoder(resp.Body).Decode(&result)

		hostPath := translateDiskPath(h.cfg.DataDir, result.Path)
		resolved = append(resolved, runtime.ResolvedDiskMount{
			HostPath: hostPath,
			Target:   dm.Target,
			ReadOnly: dm.ReadOnly,
		})
		log.Printf("resolved disk %s → %s (target=%s)", dm.DiskID, hostPath, dm.Target)
	}

	return resolved, nil
}

// translateDiskPath converts a storage-disk internal path (e.g. "/data/storage-root/shared/agent-claude")
// to a host-side path for Docker bind mounting.
func translateDiskPath(dataDir, storagePath string) string {
	cleaned := strings.TrimPrefix(storagePath, "/data/")
	return filepath.Join(dataDir, "storage-disk", cleaned)
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
	if err := h.db().Where("subdomain = ?", req.Subdomain).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subdomain %q already in use", req.Subdomain)})
		return
	}

	mc := models.ManagedContainer{
		ID:           generateContainerID(),
		PluginID:     pluginID,
		Name:         req.Name,
		Image:        req.Image,
		Port:         req.Port,
		Subdomain:    req.Subdomain,
		DockerUser:   req.DockerUser,
		PluginSource: req.PluginSource,
	}
	mc.SetEnv(req.Env)
	mc.SetCmd(req.Cmd)
	mc.SetDiskMounts(req.DiskMounts)

	if err := h.db().Create(&mc).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save container record"})
		return
	}

	// Resolve disk paths via storage-disk API.
	resolvedMounts, err := h.resolveManagedDiskPaths(c.Request.Context(), mc.GetDiskMounts())
	if err != nil {
		h.db().Delete(&mc)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to resolve disks: %v", err)})
		return
	}

	// Start the container via Docker runtime.
	if h.runtime == nil {
		h.db().Delete(&mc)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime unavailable"})
		return
	}

	containerID, err := h.runtime.StartManagedContainer(c.Request.Context(), &mc, h.cfg.BaseDomain, resolvedMounts)
	if err != nil {
		h.db().Delete(&mc)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start container: %v", err)})
		return
	}

	mc.ContainerID = containerID
	mc.Status = "running"
	h.db().Save(&mc)

	waitForReady(&mc, 30*time.Second)

	c.JSON(http.StatusCreated, mcResponse(&mc))
}

// ListManagedContainers handles GET /api/plugins/containers.
func (h *PluginHandler) ListManagedContainers(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var containers []models.ManagedContainer
	h.db().Where("plugin_id = ?", pluginID).Find(&containers)

	resp := make([]managedContainerResponse, len(containers))
	for i := range containers {
		resp[i] = mcResponse(&containers[i])
	}
	c.JSON(http.StatusOK, resp)
}

// GetManagedContainer handles GET /api/plugins/containers/:id.
func (h *PluginHandler) GetManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
		return
	}
	c.JSON(http.StatusOK, mcResponse(&mc))
}

// StopManagedContainer handles POST /api/plugins/containers/:id/stop.
// Stops the Docker container but keeps the DB record so it can be re-started.
func (h *PluginHandler) StopManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
		return
	}

	if mc.Status == "stopped" {
		c.JSON(http.StatusOK, mcResponse(&mc))
		return
	}

	if h.runtime != nil && mc.ContainerID != "" {
		if err := h.runtime.StopPlugin(c.Request.Context(), mc.ContainerID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stop container: %v", err)})
			return
		}
	}

	mc.Status = "stopped"
	mc.ContainerID = ""
	h.db().Save(&mc)

	c.JSON(http.StatusOK, mcResponse(&mc))
}

// DeleteManagedContainer handles DELETE /api/plugins/containers/:id.
func (h *PluginHandler) DeleteManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
		return
	}

	// Stop Docker container.
	if h.runtime != nil && mc.ContainerID != "" {
		if err := h.runtime.StopPlugin(c.Request.Context(), mc.ContainerID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to stop container: %v", err)})
			return
		}
	}

	h.db().Delete(&mc)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// UpdateManagedContainer handles PATCH /api/plugins/containers/:id.
// Allows renaming (name, subdomain) without stopping the container.
// Disk mounts are immutable — they reference stable storage-disk IDs.
func (h *PluginHandler) UpdateManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
		return
	}

	var req struct {
		Name      *string `json:"name"`
		Subdomain *string `json:"subdomain"`
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
		if err := h.db().Where("subdomain = ? AND id != ?", *req.Subdomain, mc.ID).First(&existing).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subdomain %q already in use", *req.Subdomain)})
			return
		}
		updates["subdomain"] = *req.Subdomain
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}

	h.db().Model(&mc).Updates(updates)
	h.db().First(&mc, "id = ?", mc.ID)
	c.JSON(http.StatusOK, mcResponse(&mc))
}

// StartManagedContainer handles POST /api/plugins/containers/:id/start.
// Re-launches a stopped container using its stored configuration.
// Resolves disk IDs via storage-disk API to get current host paths.
func (h *PluginHandler) StartManagedContainer(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
		return
	}

	if mc.Status == "running" {
		c.JSON(http.StatusOK, mcResponse(&mc))
		return
	}

	if h.runtime == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "docker runtime unavailable"})
		return
	}

	// Resolve disk IDs to current host paths via storage-disk.
	resolvedMounts, err := h.resolveManagedDiskPaths(c.Request.Context(), mc.GetDiskMounts())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to resolve disks: %v", err)})
		return
	}

	containerID, err := h.runtime.StartManagedContainer(c.Request.Context(), &mc, h.cfg.BaseDomain, resolvedMounts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to start container: %v", err)})
		return
	}

	mc.ContainerID = containerID
	mc.Status = "running"
	h.db().Save(&mc)

	waitForReady(&mc, 30*time.Second)

	c.JSON(http.StatusOK, mcResponse(&mc))
}

// GetManagedContainerLogs handles GET /api/plugins/containers/:id/logs.
func (h *PluginHandler) GetManagedContainerLogs(c *gin.Context) {
	pluginID, ok := extractPluginID(c)
	if !ok {
		return
	}

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ? AND plugin_id = ?", c.Param("cid"), pluginID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("cid"))})
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
	h.db().Find(&containers)
	c.JSON(http.StatusOK, containers)
}

// ForceDeleteManagedContainer handles DELETE /api/managed-containers/:id.
func (h *PluginHandler) ForceDeleteManagedContainer(c *gin.Context) {
	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", c.Param("id"))})
		return
	}

	if h.runtime != nil && mc.ContainerID != "" {
		_ = h.runtime.StopPlugin(c.Request.Context(), mc.ContainerID)
	}

	h.db().Delete(&mc)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// containerTargetURL returns the URL for a managed container.
// Default: http://teamagentica-mc-{id}:{port}.
func containerTargetURL(mc *models.ManagedContainer) string {
	return fmt.Sprintf("http://teamagentica-mc-%s:%d", mc.ID, mc.Port)
}

// buildProxyRequest constructs the upstream request that the proxy will send
// to the managed container. Given the container metadata, the sub-path from
// the route, and the original incoming request, it returns a new request with:
//   - URL = http://teamagentica-mc-{id}:{port}/ws/{id}{path}
//   - Host = original request Host (preserved for WebSocket Origin checks)
func buildProxyRequest(mc *models.ManagedContainer, path string, incoming *http.Request) *http.Request {
	targetURL := containerTargetURL(mc)
	fullPath := "/ws/" + mc.ID + path

	req := incoming.Clone(incoming.Context())
	parsed, _ := url.Parse(targetURL)
	req.URL = parsed
	req.URL.Path = fullPath
	req.Host = incoming.Host
	return req
}

// testContainerTargetOverride, when non-empty, overrides the container target URL in tests.
var testContainerTargetOverride string

// ProxyToManagedContainer handles ANY /ws/:container_id/*path — proxies requests
// through the kernel to the target managed container. Uses httputil.ReverseProxy
// to support both regular HTTP and WebSocket connections transparently.
func (h *PluginHandler) ProxyToManagedContainer(c *gin.Context) {
	containerID := c.Param("container_id")
	path := c.Param("path")
	start := time.Now()

	var mc models.ManagedContainer
	if err := h.db().First(&mc, "id = ?", containerID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("container %q not found", containerID)})
		return
	}

	if mc.Status != "running" || mc.ContainerID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "container not running"})
		return
	}

	targetURL := containerTargetURL(&mc)
	if testContainerTargetOverride != "" {
		targetURL = testContainerTargetOverride
	}
	target, _ := url.Parse(targetURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = h.transport

	// Forward the full path (/ws/{id}/...) to the container.
	// Containers with portpilot (devbox) use PROXY_BASE_PATH to strip the prefix.
	// Preserve original Host so WebSocket Origin==Host checks pass.
	fullPath := "/ws/" + containerID + path
	originalHost := c.Request.Host
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)
		req.URL.Path = fullPath
		req.Host = originalHost
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		// Remove upstream headers that restrict framing — the UI
		// embeds workspaces in iframes.
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("X-Frame-Options")

		var detail string
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			detail = string(body)
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.Events.Emit(events.DebugEvent{
			Type:     "ws-proxy",
			PluginID: mc.PluginID,
			Method:   c.Request.Method,
			Path:     path,
			Status:   resp.StatusCode,
			Duration: time.Since(start).Milliseconds(),
			Detail:   detail,
		})
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		h.Events.Emit(events.DebugEvent{
			Type:     "ws-proxy",
			PluginID: mc.PluginID,
			Method:   c.Request.Method,
			Path:     path,
			Status:   502,
			Duration: time.Since(start).Milliseconds(),
			Detail:   err.Error(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"Workspace container '%s' is not reachable — it may have stopped or is still starting up"}`, containerID)
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// StopManagedContainersByPlugin stops and deletes all managed containers
// owned by the given plugin. Called during plugin disable/uninstall.
func (h *PluginHandler) StopManagedContainersByPlugin(ctx context.Context, pluginID string) {
	var containers []models.ManagedContainer
	h.db().Where("plugin_id = ?", pluginID).Find(&containers)

	for _, mc := range containers {
		if h.runtime != nil && mc.ContainerID != "" {
			_ = h.runtime.StopPlugin(ctx, mc.ContainerID)
		}
		h.db().Delete(&mc)
	}
}

// ResolveDiskPaths is exported for use by the orchestrator during managed container recovery.
func (h *PluginHandler) ResolveDiskPaths(ctx context.Context, mounts []models.DiskMount) ([]runtime.ResolvedDiskMount, error) {
	return h.resolveManagedDiskPaths(ctx, mounts)
}
