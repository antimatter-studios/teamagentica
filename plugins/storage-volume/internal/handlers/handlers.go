package handlers

import (
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
	"golang.org/x/sys/unix"
)

type Handler struct {
	dataPath string
	debug    bool
}

func NewHandler(dataPath string, debug bool) *Handler {
	return &Handler{
		dataPath: dataPath,
		debug:    debug,
	}
}

type diskStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

func getDiskStats(path string) (*diskStats, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return &diskStats{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: free,
		UsedPercent:    pct,
	}, nil
}

func (h *Handler) Health(c *gin.Context) {
	stats, err := getDiskStats(h.dataPath)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"plugin": "storage-volume",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "storage-volume",
		"version":    "1.0.0",
		"disk_usage": stats,
	})
}

func (h *Handler) Usage(c *gin.Context) {
	stats, err := getDiskStats(h.dataPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	dirSizes := h.listDirSizes()

	c.JSON(http.StatusOK, gin.H{
		"disk":        stats,
		"directories": dirSizes,
	})
}

type dirInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size_bytes"`
}

func (h *Handler) Dirs(c *gin.Context) {
	dirs := h.listDirSizes()
	c.JSON(http.StatusOK, gin.H{"directories": dirs})
}

func (h *Handler) listDirSizes() []dirInfo {
	entries, err := os.ReadDir(h.dataPath)
	if err != nil {
		return nil
	}

	var dirs []dirInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		size := dirSize(filepath.Join(h.dataPath, e.Name()))
		dirs = append(dirs, dirInfo{Name: e.Name(), Size: size})
	}
	return dirs
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

// --- storage:api compatible endpoints ---

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

// resolvePath safely resolves a key to a filesystem path within dataPath.
func (h *Handler) resolvePath(key string) (string, error) {
	cleaned := filepath.Clean("/" + key)
	full := filepath.Join(h.dataPath, cleaned)
	if !strings.HasPrefix(full, filepath.Clean(h.dataPath)) {
		return "", fmt.Errorf("path traversal denied")
	}
	return full, nil
}

// Browse handles GET /browse?prefix= — directory-like listing.
func (h *Handler) Browse(c *gin.Context) {
	prefix := c.Query("prefix")

	dirPath, err := h.resolvePath(prefix)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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
			folder := prefix + e.Name() + "/"
			result.Folders = append(result.Folders, folder)
		} else {
			info, err := e.Info()
			if err != nil {
				continue
			}
			key := prefix + e.Name()
			ct := mime.TypeByExtension(filepath.Ext(e.Name()))
			if ct == "" {
				ct = "application/octet-stream"
			}
			result.Files = append(result.Files, storageFile{
				Key:          key,
				Size:         info.Size(),
				ContentType:  ct,
				LastModified: info.ModTime(),
				ETag:         fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
			})
		}
	}

	c.JSON(http.StatusOK, result)
}

// PutObject handles PUT /objects/*key — upload/write a file.
func (h *Handler) PutObject(c *gin.Context) {
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
		log.Printf("[handlers] put error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "status": "uploaded"})
}

// GetObject handles GET /objects/*key — download a file.
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

	info, err := os.Stat(fullPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(fullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

	c.Header("Content-Type", ct)
	c.Header("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	c.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	c.File(fullPath)
}

// DeleteObject handles DELETE /objects/*key — delete a file.
func (h *Handler) DeleteObject(c *gin.Context) {
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

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "status": "deleted"})
}

// HeadObject handles HEAD /objects/*key — file metadata.
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

	ct := mime.TypeByExtension(filepath.Ext(fullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

	c.Header("Content-Type", ct)
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))
	c.Header("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	c.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	c.Status(http.StatusOK)
}

// --- Tool interface for AI agents -------------------------------------------

// Tools returns the tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tools": []gin.H{
			{
				"name":        "list_files",
				"description": "List files and folders at a given path prefix in volume storage. Returns folder names and file metadata (key, size, content_type, last_modified). Use prefix '' or '/' to list root.",
				"endpoint":    "/tool/list_files",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"prefix": gin.H{"type": "string", "description": "Path prefix to list, e.g. 'projects/' or 'data/reports/'. Use empty string for root."},
					},
					"required": []string{},
				},
			},
			{
				"name":        "read_file",
				"description": "Read a file from volume storage. Returns the file content as text (for text files) or base64-encoded data (for binary files), along with metadata.",
				"endpoint":    "/tool/read_file",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"key": gin.H{"type": "string", "description": "Full path/key of the file to read, e.g. 'projects/myapp/README.md'"},
					},
					"required": []string{"key"},
				},
			},
			{
				"name":        "write_file",
				"description": "Write or overwrite a file in volume storage. Creates parent folders automatically. Use this to save code, data, configs, documents, or any content.",
				"endpoint":    "/tool/write_file",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"key":          gin.H{"type": "string", "description": "Full path/key for the file, e.g. 'projects/myapp/src/main.go'"},
						"content":      gin.H{"type": "string", "description": "File content as text (for text files) or base64-encoded string (for binary files)"},
						"content_type": gin.H{"type": "string", "description": "MIME type, e.g. 'text/plain', 'application/json', 'image/png'. Defaults to 'text/plain' for text content."},
						"encoding":     gin.H{"type": "string", "description": "Set to 'base64' if content is base64-encoded binary data. Omit for plain text.", "enum": []string{"base64", "text"}},
					},
					"required": []string{"key", "content"},
				},
			},
			{
				"name":        "delete_file",
				"description": "Delete a file from volume storage permanently.",
				"endpoint":    "/tool/delete_file",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"key": gin.H{"type": "string", "description": "Full path/key of the file to delete, e.g. 'projects/myapp/old_file.txt'"},
					},
					"required": []string{"key"},
				},
			},
			{
				"name":        "file_info",
				"description": "Get metadata about a file without downloading it. Returns size, content type, last modified time, and ETag.",
				"endpoint":    "/tool/file_info",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"key": gin.H{"type": "string", "description": "Full path/key of the file, e.g. 'projects/myapp/README.md'"},
					},
					"required": []string{"key"},
				},
			},
			{
				"name":        "create_folder",
				"description": "Create a folder (directory) in volume storage.",
				"endpoint":    "/tool/create_folder",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"path": gin.H{"type": "string", "description": "Folder path to create, e.g. 'projects/myapp/src/'. A trailing '/' is added if missing."},
					},
					"required": []string{"path"},
				},
			},
		},
	})
}

// ToolListFiles handles POST /tool/list_files.
func (h *Handler) ToolListFiles(c *gin.Context) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	dirPath, err := h.resolvePath(req.Prefix)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, browseResult{Prefix: req.Prefix})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := browseResult{Prefix: req.Prefix}
	for _, e := range entries {
		if e.IsDir() {
			result.Folders = append(result.Folders, req.Prefix+e.Name()+"/")
		} else {
			info, err := e.Info()
			if err != nil {
				continue
			}
			ct := mime.TypeByExtension(filepath.Ext(e.Name()))
			if ct == "" {
				ct = "application/octet-stream"
			}
			result.Files = append(result.Files, storageFile{
				Key:          req.Prefix + e.Name(),
				Size:         info.Size(),
				ContentType:  ct,
				LastModified: info.ModTime(),
				ETag:         fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
			})
		}
	}

	c.JSON(http.StatusOK, result)
}

// ToolReadFile handles POST /tool/read_file.
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

	ct := mime.TypeByExtension(filepath.Ext(fullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

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

// ToolWriteFile handles POST /tool/write_file.
func (h *Handler) ToolWriteFile(c *gin.Context) {
	var req struct {
		Key         string `json:"key"`
		Content     string `json:"content"`
		ContentType string `json:"content_type"`
		Encoding    string `json:"encoding"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and content are required"})
		return
	}

	fullPath, err := h.resolvePath(req.Key)
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
		log.Printf("[tool] write error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}

	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "written", "content_type": contentType})
}

// ToolDeleteFile handles POST /tool/delete_file.
func (h *Handler) ToolDeleteFile(c *gin.Context) {
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

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "deleted"})
}

// ToolFileInfo handles POST /tool/file_info.
func (h *Handler) ToolFileInfo(c *gin.Context) {
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

	info, err := os.Stat(fullPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(fullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

	c.JSON(http.StatusOK, gin.H{
		"key":           req.Key,
		"size":          info.Size(),
		"content_type":  ct,
		"last_modified": info.ModTime(),
		"etag":          fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()),
	})
}

// ToolCreateFolder handles POST /tool/create_folder.
func (h *Handler) ToolCreateFolder(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	path := req.Path
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	fullPath, err := h.resolvePath(path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		log.Printf("[tool] create folder error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"path": path, "status": "created"})
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
		"application/x-httpd-php",
	}
	for _, t := range textTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}
