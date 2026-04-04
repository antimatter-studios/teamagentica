package handlers

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// --- storage:api types ---

type storageFile struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type"`
	LastModified time.Time `json:"last_modified"`
	ETag         string    `json:"etag"`
}

type browseResult struct {
	Prefix  string        `json:"prefix"`
	Folders []string      `json:"folders"`
	Files   []storageFile `json:"files"`
}

// mimeByExt returns a MIME type for a filename based on its extension.
func mimeByExt(name string) string {
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}

// resolvePath safely resolves a key to a filesystem path within storageRoot (read-only access).
func (h *Handler) resolvePath(key string) (string, error) {
	cleaned := filepath.Clean("/" + key)
	full := filepath.Join(h.storageRoot, cleaned)
	cleanBase := filepath.Clean(h.storageRoot)
	if !strings.HasPrefix(full, cleanBase) {
		return "", fmt.Errorf("path traversal denied")
	}

	// If path exists, resolve symlinks and re-check prefix to prevent symlink bypass.
	if _, err := os.Lstat(full); err == nil {
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return "", fmt.Errorf("path traversal denied")
		}
		resolvedBase, err := filepath.EvalSymlinks(cleanBase)
		if err != nil {
			return "", fmt.Errorf("path traversal denied")
		}
		if !strings.HasPrefix(resolved, resolvedBase) {
			return "", fmt.Errorf("path traversal denied")
		}
	}

	return full, nil
}

// resolveWritablePath resolves a key and ensures it's inside a disk (type/disk/...).
// Files can only be created inside a disk, not at the storageRoot or type level.
func (h *Handler) resolveWritablePath(key string) (string, error) {
	full, err := h.resolvePath(key)
	if err != nil {
		return "", err
	}

	// Key must have at least 3 path components: <type>/<disk>/<something>
	parts := strings.Split(strings.Trim(filepath.Clean(key), "/"), "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("files can only be written inside a disk (e.g. shared/my-disk/file.txt)")
	}

	// First component must be a valid disk type.
	if err := validateDiskType(parts[0]); err != nil {
		return "", err
	}

	return full, nil
}

// --- storage:api REST endpoints ---

// Browse handles GET /browse?prefix=.
// At the root level, a virtual .trash/ folder is injected that aggregates
// the per-type .trash directories. Browsing .trash/ shows type subdirectories,
// and .trash/<type>/... maps to storageRoot/<type>/.trash/...
func (h *Handler) Browse(c *gin.Context) {
	prefix := c.Query("prefix")

	// Virtual .trash/ at root: list type subdirs that have .trash contents.
	if prefix == ".trash/" {
		result := browseResult{Prefix: prefix}
		for _, t := range []string{"shared", "workspace"} {
			trashDir := filepath.Join(h.storageRoot, t, ".trash")
			if entries, err := os.ReadDir(trashDir); err == nil && len(entries) > 0 {
				result.Folders = append(result.Folders, prefix+t+"/")
			}
		}
		c.JSON(http.StatusOK, result)
		return
	}

	// Virtual .trash/<type>/... → storageRoot/<type>/.trash/...
	if strings.HasPrefix(prefix, ".trash/") {
		rest := strings.TrimPrefix(prefix, ".trash/")
		// Extract type from first path component.
		parts := strings.SplitN(rest, "/", 2)
		diskType := parts[0]
		if err := validateDiskType(diskType); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		subPrefix := ""
		if len(parts) > 1 {
			subPrefix = parts[1]
		}
		dirPath := filepath.Join(h.storageRoot, diskType, ".trash", subPrefix)
		h.browseDir(c, prefix, dirPath, false)
		return
	}

	dirPath, err := h.resolvePath(prefix)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := h.buildBrowseResult(prefix, dirPath, true)

	// At root, inject virtual .trash/ if any type has trash contents.
	if prefix == "" {
		for _, t := range []string{"shared", "workspace"} {
			trashDir := filepath.Join(h.storageRoot, t, ".trash")
			if entries, err := os.ReadDir(trashDir); err == nil && len(entries) > 0 {
				result.Folders = append(result.Folders, ".trash/")
				break
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

// browseDir lists a directory and returns the result as JSON.
func (h *Handler) browseDir(c *gin.Context, prefix, dirPath string, skipTrash bool) {
	c.JSON(http.StatusOK, h.buildBrowseResult(prefix, dirPath, skipTrash))
}

// buildBrowseResult reads a directory and builds a browseResult.
func (h *Handler) buildBrowseResult(prefix, dirPath string, skipTrash bool) browseResult {
	result := browseResult{Prefix: prefix}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return result
	}

	for _, e := range entries {
		if skipTrash && e.Name() == ".trash" {
			continue
		}
		if e.IsDir() {
			result.Folders = append(result.Folders, prefix+e.Name()+"/")
		} else {
			info, err := e.Info()
			if err != nil {
				continue
			}
			result.Files = append(result.Files, storageFile{
				Key:          prefix + e.Name(),
				Size:         info.Size(),
				ContentType:  mimeByExt(e.Name()),
				LastModified: info.ModTime(),
				ETag:         fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
			})
		}
	}

	return result
}

// PutObject handles PUT /objects/*key.
func (h *Handler) PutObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	fullPath, err := h.resolveWritablePath(key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	f, err := os.Create(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()

	if _, err := io.Copy(f, c.Request.Body); err != nil {
		log.Printf("[storage-api] put error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "status": "uploaded"})
}

// GetObject handles GET /objects/*key.
// Uses http.ServeContent instead of c.File/http.ServeFile to avoid Go's
// built-in redirect for index.html URLs (which breaks API object retrieval).
func (h *Handler) GetObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	fullPath, err := h.resolvePath(key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	f, err := os.Open(fullPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ct := mimeByExt(fullPath)
	c.Header("Content-Type", ct)
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	http.ServeContent(c.Writer, c.Request, filepath.Base(fullPath), info.ModTime(), f)
}

// DeleteObject handles DELETE /objects/*key.
func (h *Handler) DeleteObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	fullPath, err := h.resolveWritablePath(key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := os.Stat(fullPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		log.Printf("[storage-api] delete error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "status": "deleted"})
}

// CopyObject handles POST /objects/copy — duplicate an object or folder.
func (h *Handler) CopyObject(c *gin.Context) {
	var req struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Destination == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and destination are required"})
		return
	}

	srcPath, err := h.resolvePath(req.Source)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dstPath, err := h.resolveWritablePath(req.Destination)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source not found"})
		return
	}

	if info.IsDir() {
		if err := copyDirRecursive(srcPath, dstPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		if err := copySingleFile(srcPath, dstPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "copied"})
}

func copySingleFile(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func copyDirRecursive(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copySingleFile(path, target)
	})
}

// MoveObject handles POST /objects/move — rename/move an object.
func (h *Handler) MoveObject(c *gin.Context) {
	var req struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Destination == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and destination are required"})
		return
	}

	srcPath, err := h.resolveWritablePath(req.Source)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dstPath, err := h.resolveWritablePath(req.Destination)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := os.Rename(srcPath, dstPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "moved"})
}

// HeadObject handles HEAD /objects/*key.
func (h *Handler) HeadObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	fullPath, err := h.resolvePath(key)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	ct := mimeByExt(fullPath)
	c.Header("Content-Type", ct)
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))
	c.Header("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	c.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	c.Status(http.StatusOK)
}

// DownloadZip handles GET /objects/zip?prefix= — stream a directory as a zip archive.
func (h *Handler) DownloadZip(c *gin.Context) {
	prefix := c.Query("prefix")
	if prefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prefix is required"})
		return
	}

	dirPath, err := h.resolvePath(prefix)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}

	// Use the folder name for the zip filename.
	folderName := filepath.Base(dirPath)
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, folderName))

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	_ = filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dirPath, path)
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

// --- storage:api tool endpoints for AI agents ---

// StorageAPIToolDefs returns tool definitions for the storage:api file interface.
func StorageAPIToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "list_files",
			"description": "List files and folders at a given path prefix. Returns folder names and file metadata.",
			"endpoint":    "/mcp/list_files",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prefix": gin.H{"type": "string", "description": "Path prefix to list, e.g. 'projects/' or 'data/'. Use empty string for root."},
				},
				"required": []string{},
			},
		},
		{
			"name":        "read_file",
			"description": "Read a file from storage. Returns text content or base64-encoded data for binary files.",
			"endpoint":    "/mcp/read_file",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"key": gin.H{"type": "string", "description": "Full path/key of the file to read"},
				},
				"required": []string{"key"},
			},
		},
		{
			"name":        "write_file",
			"description": "Write or overwrite a file in storage. Creates parent folders automatically.",
			"endpoint":    "/mcp/write_file",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"key":      gin.H{"type": "string", "description": "Full path/key for the file"},
					"content":  gin.H{"type": "string", "description": "File content as text or base64-encoded string"},
					"encoding": gin.H{"type": "string", "description": "Set to 'base64' for binary data. Omit for plain text.", "enum": []string{"base64", "text"}},
				},
				"required": []string{"key", "content"},
			},
		},
		{
			"name":        "delete_file",
			"description": "Permanently delete a file from storage.",
			"endpoint":    "/mcp/delete_file",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"key": gin.H{"type": "string", "description": "Full path/key of the file to delete"},
				},
				"required": []string{"key"},
			},
		},
	}
}

// ToolListFiles handles POST /mcp/list_files.
// Delegates to Browse via query params so virtual .trash/ logic is shared.
func (h *Handler) ToolListFiles(c *gin.Context) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	c.Request.URL.RawQuery = "prefix=" + req.Prefix
	h.Browse(c)
}

// ToolReadFile handles POST /mcp/read_file.
func (h *Handler) ToolReadFile(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	fullPath, err := h.resolvePath(req.Key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ct := mimeByExt(fullPath)
	if isTextContentType(ct) {
		c.JSON(http.StatusOK, gin.H{
			"key":          req.Key,
			"content":      string(data),
			"content_type": ct,
			"size":         len(data),
			"encoding":     "text",
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"key":          req.Key,
			"content":      base64.StdEncoding.EncodeToString(data),
			"content_type": ct,
			"size":         len(data),
			"encoding":     "base64",
		})
	}
}

// ToolWriteFile handles POST /mcp/write_file.
func (h *Handler) ToolWriteFile(c *gin.Context) {
	var req struct {
		Key      string `json:"key"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and content are required"})
		return
	}

	fullPath, err := h.resolveWritablePath(req.Key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var content []byte
	if req.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid base64 content"})
			return
		}
		content = decoded
	} else {
		content = []byte(req.Content)
	}

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		log.Printf("[storage-api] write error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "written"})
}

// ToolDeleteFile handles POST /mcp/delete_file.
func (h *Handler) ToolDeleteFile(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	fullPath, err := h.resolveWritablePath(req.Key)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := os.Stat(fullPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		log.Printf("[storage-api] delete error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "deleted"})
}

// --- Trash endpoints (per-type .trash) ---

// trashPath returns the .trash directory for a disk type.
func (h *Handler) trashPath(diskType string) string {
	return filepath.Join(h.storageRoot, diskType, ".trash")
}

// resolveTrashPath safely resolves a key to a path within a type's .trash.
func (h *Handler) resolveTrashPath(diskType, key string) (string, error) {
	cleaned := filepath.Clean("/" + key)
	trashBase := h.trashPath(diskType)
	full := filepath.Join(trashBase, cleaned)
	if !strings.HasPrefix(full, filepath.Clean(trashBase)) {
		return "", fmt.Errorf("path traversal denied")
	}
	return full, nil
}

// BrowseTrash handles GET /trash/:type/browse?prefix=.
func (h *Handler) BrowseTrash(c *gin.Context) {
	diskType := c.Param("type")
	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	prefix := c.Query("prefix")
	dirPath := h.trashPath(diskType)
	if prefix != "" {
		var err error
		dirPath, err = h.resolveTrashPath(diskType, prefix)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, browseResult{Prefix: prefix})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := browseResult{Prefix: prefix}
	for _, e := range entries {
		if e.IsDir() {
			result.Folders = append(result.Folders, prefix+e.Name()+"/")
		} else {
			info, err := e.Info()
			if err != nil {
				continue
			}
			result.Files = append(result.Files, storageFile{
				Key:          prefix + e.Name(),
				Size:         info.Size(),
				ContentType:  mimeByExt(e.Name()),
				LastModified: info.ModTime(),
				ETag:         fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
			})
		}
	}

	c.JSON(http.StatusOK, result)
}

// RestoreTrash handles POST /trash/:type/restore — move a disk from .trash back to its type directory.
func (h *Handler) RestoreTrash(c *gin.Context) {
	diskType := c.Param("type")
	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	trashItemPath, err := h.resolveTrashPath(diskType, req.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := os.Stat(trashItemPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("not found in trash: %s", req.Name)})
		return
	}

	restorePath := h.diskDataPath(diskType, req.Name)
	if _, err := os.Stat(restorePath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("disk %q already exists", req.Name)})
		return
	}

	if err := os.Rename(trashItemPath, restorePath); err != nil {
		// Fallback: copy + delete (cross-device).
		if cpErr := copyDirRecursive(trashItemPath, restorePath); cpErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": cpErr.Error()})
			return
		}
		os.RemoveAll(trashItemPath)
	}

	log.Printf("[storage] restored from trash: %s/%s", diskType, req.Name)
	c.JSON(http.StatusOK, gin.H{"name": req.Name, "type": diskType, "status": "restored"})
}

// EmptyTrash handles POST /trash/:type/empty — permanently delete trash contents.
func (h *Handler) EmptyTrash(c *gin.Context) {
	diskType := c.Param("type")
	if err := validateDiskType(diskType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	c.ShouldBindJSON(&req)

	if req.Name != "" {
		trashItemPath, err := h.resolveTrashPath(diskType, req.Name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := os.Stat(trashItemPath); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("not found in trash: %s", req.Name)})
			return
		}
		if err := os.RemoveAll(trashItemPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		log.Printf("[storage] permanently deleted from trash: %s/%s", diskType, req.Name)
		c.JSON(http.StatusOK, gin.H{"name": req.Name, "type": diskType, "status": "permanently_deleted"})
		return
	}

	// Empty entire type trash.
	trashBase := h.trashPath(diskType)
	if err := os.RemoveAll(trashBase); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[storage] emptied %s trash", diskType)
	c.JSON(http.StatusOK, gin.H{"type": diskType, "status": "trash_emptied"})
}

// TrashToolDefs returns tool definitions for trash management.
func TrashToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "browse_trash",
			"description": "Browse deleted disks in the trash for a given type (shared or workspace).",
			"endpoint":    "/mcp/browse_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"type":   gin.H{"type": "string", "description": "Disk type: 'shared' or 'workspace'.", "enum": []string{"shared", "workspace"}},
					"prefix": gin.H{"type": "string", "description": "Path prefix within trash to browse. Use empty string for trash root."},
				},
				"required": []string{"type"},
			},
		},
		{
			"name":        "restore_from_trash",
			"description": "Restore a deleted disk from trash back to its type directory.",
			"endpoint":    "/mcp/restore_from_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"type": gin.H{"type": "string", "description": "Disk type: 'shared' or 'workspace'.", "enum": []string{"shared", "workspace"}},
					"name": gin.H{"type": "string", "description": "Name of the disk to restore from trash"},
				},
				"required": []string{"type", "name"},
			},
		},
		{
			"name":        "empty_trash",
			"description": "Permanently delete disks from trash. Specify a name to delete one item, or omit to empty all trash for the type.",
			"endpoint":    "/mcp/empty_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"type": gin.H{"type": "string", "description": "Disk type: 'shared' or 'workspace'.", "enum": []string{"shared", "workspace"}},
					"name": gin.H{"type": "string", "description": "Optional: specific disk name to permanently delete from trash. Omit to empty all trash for the type."},
				},
				"required": []string{"type"},
			},
		},
	}
}

// ToolBrowseTrash handles POST /mcp/browse_trash.
func (h *Handler) ToolBrowseTrash(c *gin.Context) {
	var req struct {
		Type   string `json:"type"`
		Prefix string `json:"prefix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "type", Value: req.Type})
	if req.Prefix != "" {
		c.Request.URL.RawQuery = "prefix=" + req.Prefix
	}
	h.BrowseTrash(c)
}

// ToolRestoreFromTrash handles POST /mcp/restore_from_trash.
func (h *Handler) ToolRestoreFromTrash(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Type == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type and name are required"})
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "type", Value: req.Type})
	h.RestoreTrash(c)
}

// ToolEmptyTrash handles POST /mcp/empty_trash.
func (h *Handler) ToolEmptyTrash(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "type", Value: req.Type})
	h.EmptyTrash(c)
}

// isTextContentType returns true if the MIME type suggests text content.
func isTextContentType(ct string) bool {
	if ct == "" {
		return true
	}
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/typescript",
		"application/x-yaml",
		"application/yaml",
		"application/toml",
		"application/x-sh",
		"application/sql",
		"application/graphql",
		"application/xhtml+xml",
	}
	for _, t := range textTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}
