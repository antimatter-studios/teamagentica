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
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`       // "shared" or "workspace"
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
	case "shared", "workspace":
		return nil
	default:
		return fmt.Errorf("invalid disk type %q: must be 'shared' or 'workspace'", t)
	}
}

// typePath returns the directory for a disk type: storageRoot/shared or storageRoot/workspace.
func (h *Handler) typePath(diskType string) string {
	return filepath.Join(h.storageRoot, diskType)
}

// diskDataPath returns the data directory path for a disk: storageRoot/<type>/<name>.
func (h *Handler) diskDataPath(diskType, name string) string {
	return filepath.Join(h.storageRoot, diskType, name)
}

// findDiskType looks up the type for a disk by name from the database,
// falling back to checking both type directories on disk.
func (h *Handler) findDiskType(name string) string {
	var rec DiskRecord
	if err := h.db.First(&rec, "name = ?", name).Error; err == nil {
		return rec.Type
	}
	// Fallback: check filesystem.
	for _, t := range []string{"shared", "workspace"} {
		if _, err := os.Stat(filepath.Join(h.storageRoot, t, name)); err == nil {
			return t
		}
	}
	return "workspace"
}

// loadDiskMeta reads disk metadata from the database.
func (h *Handler) loadDiskMeta(name string) (*Disk, error) {
	var rec DiskRecord
	if err := h.db.First(&rec, "name = ?", name).Error; err != nil {
		return nil, err
	}
	d := &Disk{
		ID:        rec.ID,
		Name:      rec.Name,
		Type:      rec.Type,
		CreatedAt: rec.CreatedAt,
	}
	if rec.Labels != "" {
		json.Unmarshal([]byte(rec.Labels), &d.Labels)
	}
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}
	return d, nil
}

// saveDiskMeta writes disk metadata to the database.
func (h *Handler) saveDiskMeta(d *Disk) error {
	labelsJSON, _ := json.Marshal(d.Labels)
	rec := DiskRecord{
		ID:        d.ID,
		Name:      d.Name,
		Type:      d.Type,
		Labels:    string(labelsJSON),
		CreatedAt: d.CreatedAt,
	}
	return h.db.Save(&rec).Error
}

// deleteDiskMeta removes disk metadata from the database.
func (h *Handler) deleteDiskMeta(name string) error {
	return h.db.Delete(&DiskRecord{}, "name = ?", name).Error
}

// diskMetaExists checks if a disk has metadata in the database.
func (h *Handler) diskMetaExists(name string) bool {
	var count int64
	h.db.Model(&DiskRecord{}).Where("name = ?", name).Count(&count)
	return count > 0
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
		req.Type = "workspace"
	}
	if err := validateDiskType(req.Type); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if disk already exists.
	if h.diskMetaExists(req.Name) {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("disk %q already exists", req.Name)})
		return
	}

	// Create data directory.
	dataDir := h.diskDataPath(req.Type, req.Name)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("[disks] create dir error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create disk directory"})
		return
	}

	// Save metadata.
	d := &Disk{
		ID:        generateDiskID(),
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

	c.JSON(http.StatusCreated, gin.H{
		"id":         d.ID,
		"name":       d.Name,
		"type":       d.Type,
		"labels":     d.Labels,
		"created_at": d.CreatedAt,
		"path":       dataDir,
	})
}

// ListDisks handles GET /disks.
// Discovers disks from both the database and on-disk directories,
// so disks created externally (e.g. by workspace-manager) are visible.
func (h *Handler) ListDisks(c *gin.Context) {
	filterType := c.Query("type")

	// Collect disks with metadata from DB.
	seen := make(map[string]bool)
	var disks []DiskDetail

	var recs []DiskRecord
	q := h.db
	if filterType != "" {
		q = q.Where("type = ?", filterType)
	}
	q.Find(&recs)
	for _, rec := range recs {
		d := Disk{ID: rec.ID, Name: rec.Name, Type: rec.Type, CreatedAt: rec.CreatedAt}
		if rec.Labels != "" {
			json.Unmarshal([]byte(rec.Labels), &d.Labels)
		}
		if d.Labels == nil {
			d.Labels = map[string]string{}
		}
		seen[rec.Name] = true
		disks = append(disks, DiskDetail{
			Disk:      d,
			SizeBytes: dirSize(h.diskDataPath(rec.Type, rec.Name)),
			Path:      h.diskDataPath(rec.Type, rec.Name),
		})
	}

	// Also scan both type directories for directories without metadata.
	for _, diskType := range []string{"shared", "workspace"} {
		if filterType != "" && filterType != diskType {
			continue
		}
		dirEntries, err := os.ReadDir(h.typePath(diskType))
		if err != nil {
			continue
		}
		for _, e := range dirEntries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || seen[e.Name()] {
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
					Type:      diskType,
					Labels:    map[string]string{},
					CreatedAt: createdAt,
				},
				SizeBytes: dirSize(h.diskDataPath(diskType, e.Name())),
				Path:      h.diskDataPath(diskType, e.Name()),
			})
		}
	}

	if disks == nil {
		disks = []DiskDetail{}
	}

	c.JSON(http.StatusOK, gin.H{"disks": disks})
}

// GetDisk handles GET /disks/:type/:name.
func (h *Handler) GetDisk(c *gin.Context) {
	diskType := c.Param("type")
	name := c.Param("name")

	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dataPath := h.diskDataPath(diskType, name)
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %s/%s not found", diskType, name)})
		return
	}

	d, err := h.loadDiskMeta(name)
	if err != nil {
		// No metadata in DB — construct from filesystem.
		d = &Disk{Name: name, Type: diskType, Labels: map[string]string{}}
	}

	detail := DiskDetail{
		Disk:      *d,
		SizeBytes: dirSize(dataPath),
		Path:      dataPath,
	}

	c.JSON(http.StatusOK, detail)
}

// GetDiskPath handles GET /disks/:type/:name/path.
func (h *Handler) GetDiskPath(c *gin.Context) {
	diskType := c.Param("type")
	name := c.Param("name")

	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dataPath := h.diskDataPath(diskType, name)
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %s/%s not found", diskType, name)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "type": diskType, "path": dataPath})
}

// GetDiskByID handles GET /disks/by-id/:id.
// Resolves a stable disk ID to its current name, type, and path.
func (h *Handler) GetDiskByID(c *gin.Context) {
	id := c.Param("id")

	var rec DiskRecord
	if err := h.db.First(&rec, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk with id %q not found", id)})
		return
	}

	d := Disk{ID: rec.ID, Name: rec.Name, Type: rec.Type, CreatedAt: rec.CreatedAt}
	if rec.Labels != "" {
		json.Unmarshal([]byte(rec.Labels), &d.Labels)
	}
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}

	c.JSON(http.StatusOK, DiskDetail{
		Disk:      d,
		SizeBytes: dirSize(h.diskDataPath(rec.Type, rec.Name)),
		Path:      h.diskDataPath(rec.Type, rec.Name),
	})
}

// RenameDisk handles PATCH /disks/:type/:name.
// Renames the disk directory and updates the metadata. The stable ID is preserved.
func (h *Handler) RenameDisk(c *gin.Context) {
	diskType := c.Param("type")
	name := c.Param("name")

	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if err := validateDiskName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name == name {
		// No-op — return current state.
		h.GetDisk(c)
		return
	}

	// Check new name doesn't collide.
	newPath := h.diskDataPath(diskType, req.Name)
	if _, err := os.Stat(newPath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("disk %q already exists", req.Name)})
		return
	}

	oldPath := h.diskDataPath(diskType, name)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("disk %s/%s not found", diskType, name)})
		return
	}

	// Rename directory.
	if err := os.Rename(oldPath, newPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename disk: " + err.Error()})
		return
	}

	// Update DB record: delete old key, insert new (primary key is Name).
	var rec DiskRecord
	if err := h.db.First(&rec, "name = ?", name).Error; err == nil {
		h.db.Delete(&rec)
		rec.Name = req.Name
		h.db.Create(&rec)
	}

	log.Printf("[storage-disk] renamed disk %s/%s -> %s", diskType, name, req.Name)

	d := Disk{ID: rec.ID, Name: req.Name, Type: diskType, CreatedAt: rec.CreatedAt}
	if rec.Labels != "" {
		json.Unmarshal([]byte(rec.Labels), &d.Labels)
	}
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}
	c.JSON(http.StatusOK, DiskDetail{
		Disk:      d,
		SizeBytes: dirSize(newPath),
		Path:      newPath,
	})
}

// DeleteDisk handles DELETE /disks/:type/:name.
func (h *Handler) DeleteDisk(c *gin.Context) {
	diskType := c.Param("type")
	name := c.Param("name")

	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateDiskName(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Move disk data to trash before deleting.
	typeDir := h.typePath(diskType)
	diskPath := h.diskDataPath(diskType, name)
	if _, err := os.Stat(diskPath); err == nil {
		if err := h.moveToTrash(typeDir, diskPath); err != nil {
			log.Printf("[disks] trash data error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to trash disk data"})
			return
		}
	}

	// Remove metadata from database.
	if err := h.deleteDiskMeta(name); err != nil {
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
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if req.Type == "" {
		req.Type = h.findDiskType(req.Name)
	}
	c.Params = append(c.Params, gin.Param{Key: "type", Value: req.Type})
	c.Params = append(c.Params, gin.Param{Key: "name", Value: req.Name})
	h.DeleteDisk(c)
}

// DiskToolDefs returns tool definitions for disk management.
func DiskToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "create_disk",
			"description": "Create a new disk. Use type 'shared' for persistent state shared across workspaces (e.g. auth tokens, config) or 'workspace' for per-workspace project files.",
			"endpoint":    "/mcp/create_disk",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":   gin.H{"type": "string", "description": "Disk name (alphanumeric, hyphens, underscores, dots). E.g. 'claude-shared', 'ws-abc123-myproject'"},
					"type":   gin.H{"type": "string", "description": "Disk type: 'shared' for cross-workspace persistent state or 'workspace' for per-workspace data. Defaults to 'workspace'.", "enum": []string{"shared", "workspace"}},
					"labels": gin.H{"type": "object", "description": "Optional key-value labels for the disk, e.g. {\"service\": \"claude\", \"environment\": \"workspace-env-claude-terminal\"}"},
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
					"type": gin.H{"type": "string", "description": "Filter by disk type: 'shared' or 'workspace'. Omit to list all.", "enum": []string{"shared", "workspace"}},
				},
				"required": []string{},
			},
		},
		{
			"name":        "delete_disk",
			"description": "Delete a disk storage disk. Contents are moved to .trash before removal and can be recovered.",
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
