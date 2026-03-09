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

// resolvePath safely resolves a key to a filesystem path within dataPath.
func (h *Handler) resolvePath(key string) (string, error) {
	cleaned := filepath.Clean("/" + key)
	full := filepath.Join(h.dataPath, cleaned)
	if !strings.HasPrefix(full, filepath.Clean(h.dataPath)) {
		return "", fmt.Errorf("path traversal denied")
	}
	return full, nil
}

// --- storage:api REST endpoints ---

// Browse handles GET /browse?prefix=.
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

// PutObject handles PUT /objects/*key.
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
		log.Printf("[storage-api] put error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "status": "uploaded"})
}

// GetObject handles GET /objects/*key.
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

	ct := mimeByExt(fullPath)
	c.Header("Content-Type", ct)
	c.Header("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size()))
	c.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	c.File(fullPath)
}

// DeleteObject handles DELETE /objects/*key.
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

// --- storage:api tool endpoints for AI agents ---

// StorageAPIToolDefs returns tool definitions for the storage:api file interface.
func StorageAPIToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "list_files",
			"description": "List files and folders at a given path prefix. Returns folder names and file metadata.",
			"endpoint":    "/tool/list_files",
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
			"endpoint":    "/tool/read_file",
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
			"endpoint":    "/tool/write_file",
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
			"description": "Delete a file from storage permanently.",
			"endpoint":    "/tool/delete_file",
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
			result.Files = append(result.Files, storageFile{
				Key:          req.Prefix + e.Name(),
				Size:         info.Size(),
				ContentType:  mimeByExt(e.Name()),
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

// ToolWriteFile handles POST /tool/write_file.
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
		log.Printf("[storage-api] write error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "written"})
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
