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

// Disk represents a namespace-isolated disk storage disk.
type Disk struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`       // "auth" or "storage"
	Labels    map[string]string `json:"labels"`     // e.g. {"service": "claude", "plugin": "agent-claude"}
	CreatedAt time.Time         `json:"created_at"`
}

var diskNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validateDiskName(name string) error {
	if !diskNameRe.MatchString(name) {
		return fmt.Errorf("invalid disk name: must be 1-128 chars, alphanumeric/hyphens/underscores/dots, starting with alphanumeric")
	}
	return nil
}

func validateDiskType(t string) error {
	switch t {
	case "auth", "storage":
		return nil
	default:
		return fmt.Errorf("invalid disk type %q: must be 'auth' or 'storage'", t)
	}
}

// metaPath returns the path to the disk's metadata JSON file.
// Metadata is stored under dataPath/meta (not disksPath) to keep
// the disks directory clean for bind-mounting.
func (h *Handler) metaPath(name string) string {
	return filepath.Join(h.dataPath, "meta", name+".json")
}

// diskDataPath returns the data directory path for a disk.
func (h *Handler) diskDataPath(name string) string {
	return filepath.Join(h.disksPath, name)
}

// loadDiskMeta reads and parses a disk's metadata file.
func (h *Handler) loadDiskMeta(name string) (*Disk, error) {
	data, err := os.ReadFile(h.metaPath(name))
	if err != nil {
		return nil, err
	}
	var d Disk
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse disk meta %s: %w", name, err)
	}
	return &d, nil
}

// saveDiskMeta writes disk metadata to the filesystem.
func (h *Handler) saveDiskMeta(d *Disk) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal disk meta: %w", err)
	}
	return os.WriteFile(h.metaPath(d.Name), data, 0644)
}

// DiskDetail extends Disk with runtime info.
type DiskDetail struct {
	Disk
	SizeBytes int64  `json:"size_bytes"`
	Path      string `json:"path"`
}

// CreateDisk handles POST /disks.
func (h *Handler) CreateDisk(c *gin.Context) {
	var req struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := validateDiskName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type == "" {
		req.Type = "storage"
	}
	if err := validateDiskType(req.Type); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if disk already exists.
	if _, err := os.Stat(h.metaPath(req.Name)); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("disk %q already exists", req.Name)})
		return
	}

	// Create data directory.
	dataDir := h.diskDataPath(req.Name)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[disks] create dir error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create disk directory"})
		return
	}

	// Save metadata.
	d := &Disk{
		Name:      req.Name,
		Type:      req.Type,
		Labels:    req.Labels,
		CreatedAt: time.Now().UTC(),
	}
	if d.Labels == nil {
		d.Labels = make(map[string]string)
	}

	if err := h.saveDiskMeta(d); err != nil {
		log.Printf("[disks] save meta error: %v", err)
		os.RemoveAll(dataDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save disk metadata"})
		return
	}

	if h.debug {
		log.Printf("[disks] created disk %q (type=%s)", req.Name, req.Type)
	}

	c.JSON(http.StatusCreated, d)
}

// ListDisks handles GET /disks.
// Discovers disks from both metadata files and on-disk directories,
// so disks created externally (e.g. by workspace-manager) are visible.
func (h *Handler) ListDisks(c *gin.Context) {
	filterType := c.Query("type")

	// Collect disks with metadata.
	seen := make(map[string]bool)
	var disks []DiskDetail

	metaDir := filepath.Join(h.dataPath, "meta")
	if entries, err := os.ReadDir(metaDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".json")
			d, err := h.loadDiskMeta(name)
			if err != nil {
				log.Printf("[disks] skip unreadable meta %s: %v", name, err)
				continue
			}
			if filterType != "" && d.Type != filterType {
				continue
			}
			seen[name] = true
			disks = append(disks, DiskDetail{
				Disk:      *d,
				SizeBytes: dirSize(h.diskDataPath(name)),
				Path:      h.diskDataPath(name),
			})
		}
	}

	// Also scan the disks directory for directories without metadata.
	if dirEntries, err := os.ReadDir(h.disksPath); err == nil {
		for _, e := range dirEntries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || seen[e.Name()] {
				continue
			}
			// Infer type from name convention; skip type filter for unmanaged disks
			// unless filtering for "storage" (the default type).
			if filterType != "" && filterType != "storage" {
				continue
			}
			info, _ := e.Info()
			var createdAt time.Time
			if info != nil {
				createdAt = info.ModTime().UTC()
			}
			disks = append(disks, DiskDetail{
				Disk: Disk{
					Name:      e.Name(),
					Type:      "storage",
					Labels:    map[string]string{},
					CreatedAt: createdAt,
				},
				SizeBytes: dirSize(h.diskDataPath(e.Name())),
				Path:      h.diskDataPath(e.Name()),
			})
		}
	}

	if disks == nil {
		disks = []DiskDetail{}
	}

	c.JSON(http.StatusOK, gin.H{"disks": disks})
}

// GetDisk handles GET /disks/:name.
func (h *Handler) GetDisk(c *gin.Context) {
	name := c.Param("name")
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	d, err := h.loadDiskMeta(name)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	detail := DiskDetail{
		Disk:      *d,
		SizeBytes: dirSize(h.diskDataPath(name)),
		Path:      h.diskDataPath(name),
	}

	c.JSON(http.StatusOK, detail)
}

// GetDiskPath handles GET /disks/:name/path.
func (h *Handler) GetDiskPath(c *gin.Context) {
	name := c.Param("name")
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := h.loadDiskMeta(name); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "path": h.diskDataPath(name)})
}

// DeleteDisk handles DELETE /disks/:name.
func (h *Handler) DeleteDisk(c *gin.Context) {
	name := c.Param("name")
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := h.loadDiskMeta(name); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Move disk data to trash before deleting.
	diskPath := h.diskDataPath(name)
	if _, err := os.Stat(diskPath); err == nil {
		if err := h.moveToTrash(h.disksPath, diskPath); err != nil {
			log.Printf("[disks] trash data error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to trash disk data"})
			return
		}
	}

	// Remove metadata (not trashed — it's just a small JSON pointer).
	if err := os.Remove(h.metaPath(name)); err != nil && !os.IsNotExist(err) {
		log.Printf("[disks] delete meta error: %v", err)
	}

	if h.debug {
		log.Printf("[disks] deleted disk %q", name)
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "status": "deleted"})
}

// --- Disk tool endpoints for AI agents ---

// ToolCreateDisk handles POST /mcp/create_disk.
func (h *Handler) ToolCreateDisk(c *gin.Context) {
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
	h.CreateDisk(c)
}

// ToolListDisks handles POST /mcp/list_disks.
func (h *Handler) ToolListDisks(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	c.ShouldBindJSON(&req)

	if req.Type != "" {
		c.Request.URL.RawQuery = "type=" + req.Type
	}
	h.ListDisks(c)
}

// ToolDeleteDisk handles POST /mcp/delete_disk.
func (h *Handler) ToolDeleteDisk(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	c.Params = append(c.Params, gin.Param{Key: "name", Value: req.Name})
	h.DeleteDisk(c)
}

// DiskToolDefs returns tool definitions for disk management.
func DiskToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "create_disk",
			"description": "Create a new namespace-isolated disk storage disk. Use type 'auth' for credential storage (read-only by default) or 'storage' for general purpose.",
			"endpoint":    "/mcp/create_disk",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":   gin.H{"type": "string", "description": "Disk name (alphanumeric, hyphens, underscores, dots). E.g. 'claude-auth', 'workspace-task-123'"},
					"type":   gin.H{"type": "string", "description": "Disk type: 'auth' for credentials or 'storage' for general data. Defaults to 'storage'.", "enum": []string{"auth", "storage"}},
					"labels": gin.H{"type": "object", "description": "Optional key-value labels for the disk, e.g. {\"service\": \"claude\", \"plugin\": \"agent-claude\"}"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "list_disks",
			"description": "List all disk storage disks with their metadata, size, and filesystem path. Optionally filter by type.",
			"endpoint":    "/mcp/list_disks",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"type": gin.H{"type": "string", "description": "Filter by disk type: 'auth' or 'storage'. Omit to list all.", "enum": []string{"auth", "storage"}},
				},
				"required": []string{},
			},
		},
		{
			"name":        "delete_disk",
			"description": "Delete a disk storage disk. Contents are moved to .Trash before removal and can be recovered.",
			"endpoint":    "/mcp/delete_disk",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name": gin.H{"type": "string", "description": "Name of the disk to delete"},
				},
				"required": []string{"name"},
			},
		},
	}
}
