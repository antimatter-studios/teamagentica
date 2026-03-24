package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Volume represents a namespace-isolated disk storage volume.
type Volume struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`       // "auth" or "storage"
	Labels    map[string]string `json:"labels"`     // e.g. {"service": "claude", "plugin": "agent-claude"}
	CreatedAt time.Time         `json:"created_at"`
}

var volumeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validateVolumeName(name string) error {
	if !volumeNameRe.MatchString(name) {
		return fmt.Errorf("invalid volume name: must be 1-128 chars, alphanumeric/hyphens/underscores/dots, starting with alphanumeric")
	}
	return nil
}

func validateVolumeType(t string) error {
	switch t {
	case "auth", "storage":
		return nil
	default:
		return fmt.Errorf("invalid volume type %q: must be 'auth' or 'storage'", t)
	}
}

// metaPath returns the path to the volume's metadata JSON file.
// Metadata is stored under dataPath/meta (not volumesPath) to keep
// the volumes directory clean for bind-mounting.
func (h *Handler) metaPath(name string) string {
	return filepath.Join(h.dataPath, "meta", name+".json")
}

// volumeDataPath returns the data directory path for a volume.
func (h *Handler) volumeDataPath(name string) string {
	return filepath.Join(h.volumesPath, name)
}

// loadVolumeMeta reads and parses a volume's metadata file.
func (h *Handler) loadVolumeMeta(name string) (*Volume, error) {
	data, err := os.ReadFile(h.metaPath(name))
	if err != nil {
		return nil, err
	}
	var vol Volume
	if err := json.Unmarshal(data, &vol); err != nil {
		return nil, fmt.Errorf("parse volume meta %s: %w", name, err)
	}
	return &vol, nil
}

// saveVolumeMeta writes volume metadata to disk.
func (h *Handler) saveVolumeMeta(vol *Volume) error {
	data, err := json.MarshalIndent(vol, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal volume meta: %w", err)
	}
	return os.WriteFile(h.metaPath(vol.Name), data, 0644)
}

// VolumeDetail extends Volume with runtime info.
type VolumeDetail struct {
	Volume
	SizeBytes int64  `json:"size_bytes"`
	Path      string `json:"path"`
}

// CreateVolume handles POST /volumes.
func (h *Handler) CreateVolume(c *gin.Context) {
	var req struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := validateVolumeName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type == "" {
		req.Type = "storage"
	}
	if err := validateVolumeType(req.Type); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if volume already exists.
	if _, err := os.Stat(h.metaPath(req.Name)); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("volume %q already exists", req.Name)})
		return
	}

	// Create data directory.
	dataDir := h.volumeDataPath(req.Name)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[volumes] create dir error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create volume directory"})
		return
	}

	// Save metadata.
	vol := &Volume{
		Name:      req.Name,
		Type:      req.Type,
		Labels:    req.Labels,
		CreatedAt: time.Now().UTC(),
	}
	if vol.Labels == nil {
		vol.Labels = make(map[string]string)
	}

	if err := h.saveVolumeMeta(vol); err != nil {
		log.Printf("[volumes] save meta error: %v", err)
		os.RemoveAll(dataDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save volume metadata"})
		return
	}

	if h.debug {
		log.Printf("[volumes] created volume %q (type=%s)", req.Name, req.Type)
	}

	c.JSON(http.StatusCreated, vol)
}

// ListVolumes handles GET /volumes.
// Discovers volumes from both metadata files and on-disk directories,
// so volumes created externally (e.g. by workspace-manager) are visible.
func (h *Handler) ListVolumes(c *gin.Context) {
	filterType := c.Query("type")

	// Collect volumes with metadata.
	seen := make(map[string]bool)
	var volumes []VolumeDetail

	metaDir := filepath.Join(h.dataPath, "meta")
	if entries, err := os.ReadDir(metaDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".json")
			vol, err := h.loadVolumeMeta(name)
			if err != nil {
				log.Printf("[volumes] skip unreadable meta %s: %v", name, err)
				continue
			}
			if filterType != "" && vol.Type != filterType {
				continue
			}
			seen[name] = true
			volumes = append(volumes, VolumeDetail{
				Volume:    *vol,
				SizeBytes: dirSize(h.volumeDataPath(name)),
				Path:      h.volumeDataPath(name),
			})
		}
	}

	// Also scan the volumes directory for directories without metadata.
	if dirEntries, err := os.ReadDir(h.volumesPath); err == nil {
		for _, e := range dirEntries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || seen[e.Name()] {
				continue
			}
			// Infer type from name convention; skip type filter for unmanaged volumes
			// unless filtering for "storage" (the default type).
			if filterType != "" && filterType != "storage" {
				continue
			}
			info, _ := e.Info()
			var createdAt time.Time
			if info != nil {
				createdAt = info.ModTime().UTC()
			}
			volumes = append(volumes, VolumeDetail{
				Volume: Volume{
					Name:      e.Name(),
					Type:      "storage",
					Labels:    map[string]string{},
					CreatedAt: createdAt,
				},
				SizeBytes: dirSize(h.volumeDataPath(e.Name())),
				Path:      h.volumeDataPath(e.Name()),
			})
		}
	}

	if volumes == nil {
		volumes = []VolumeDetail{}
	}

	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

// GetVolume handles GET /volumes/:name.
func (h *Handler) GetVolume(c *gin.Context) {
	name := c.Param("name")
	if err := validateVolumeName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	vol, err := h.loadVolumeMeta(name)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("volume %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	detail := VolumeDetail{
		Volume:    *vol,
		SizeBytes: dirSize(h.volumeDataPath(name)),
		Path:      h.volumeDataPath(name),
	}

	c.JSON(http.StatusOK, detail)
}

// GetVolumePath handles GET /volumes/:name/path.
func (h *Handler) GetVolumePath(c *gin.Context) {
	name := c.Param("name")
	if err := validateVolumeName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := h.loadVolumeMeta(name); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("volume %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "path": h.volumeDataPath(name)})
}

// DeleteVolume handles DELETE /volumes/:name.
func (h *Handler) DeleteVolume(c *gin.Context) {
	name := c.Param("name")
	if err := validateVolumeName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := h.loadVolumeMeta(name); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("volume %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Move volume data to trash before deleting.
	volDataPath := h.volumeDataPath(name)
	if _, err := os.Stat(volDataPath); err == nil {
		if err := h.moveToTrash(h.volumesPath, volDataPath); err != nil {
			log.Printf("[volumes] trash data error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to trash volume data"})
			return
		}
	}

	// Remove metadata (not trashed — it's just a small JSON pointer).
	if err := os.Remove(h.metaPath(name)); err != nil && !os.IsNotExist(err) {
		log.Printf("[volumes] delete meta error: %v", err)
	}

	if h.debug {
		log.Printf("[volumes] deleted volume %q", name)
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "status": "deleted"})
}

// --- Volume tool endpoints for AI agents ---

// ToolCreateVolume handles POST /tool/create_volume.
func (h *Handler) ToolCreateVolume(c *gin.Context) {
	var req struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	// Delegate to the REST handler by rewriting context.
	c.Set("tool_request", true)
	c.Request = c.Request.Clone(c.Request.Context())
	h.CreateVolume(c)
}

// ToolListVolumes handles POST /tool/list_volumes.
func (h *Handler) ToolListVolumes(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	c.ShouldBindJSON(&req)

	if req.Type != "" {
		c.Request.URL.RawQuery = "type=" + req.Type
	}
	h.ListVolumes(c)
}

// ToolDeleteVolume handles POST /tool/delete_volume.
func (h *Handler) ToolDeleteVolume(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "name", Value: req.Name})
	h.DeleteVolume(c)
}

// VolumeToolDefs returns tool definitions for volume management.
func VolumeToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "create_volume",
			"description": "Create a new namespace-isolated disk storage volume. Use type 'auth' for credential storage (read-only by default) or 'storage' for general purpose.",
			"endpoint":    "/tool/create_volume",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":   gin.H{"type": "string", "description": "Volume name (alphanumeric, hyphens, underscores, dots). E.g. 'claude-auth', 'workspace-task-123'"},
					"type":   gin.H{"type": "string", "description": "Volume type: 'auth' for credentials or 'storage' for general data. Defaults to 'storage'.", "enum": []string{"auth", "storage"}},
					"labels": gin.H{"type": "object", "description": "Optional key-value labels for the volume, e.g. {\"service\": \"claude\", \"plugin\": \"agent-claude\"}"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "list_volumes",
			"description": "List all disk storage volumes with their metadata, size, and filesystem path. Optionally filter by type.",
			"endpoint":    "/tool/list_volumes",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"type": gin.H{"type": "string", "description": "Filter by volume type: 'auth' or 'storage'. Omit to list all.", "enum": []string{"auth", "storage"}},
				},
				"required": []string{},
			},
		},
		{
			"name":        "delete_volume",
			"description": "Delete a disk storage volume. Contents are moved to .Trash before removal and can be recovered.",
			"endpoint":    "/tool/delete_volume",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name": gin.H{"type": "string", "description": "Name of the volume to delete"},
				},
				"required": []string{"name"},
			},
		},
	}
}
