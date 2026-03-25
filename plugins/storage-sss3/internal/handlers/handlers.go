package handlers

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/index"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/s3client"
)

type Handler struct {
	client *s3client.Client
	index  *index.Index
	sdk    *pluginsdk.Client
}

func NewHandler(client *s3client.Client, idx *index.Index) *Handler {
	return &Handler{client: client, index: idx}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

// Health returns a simple health check.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "cached_objects": h.index.Count()})
}

// PutObject handles PUT /objects/*key — upload an object.
func (h *Handler) PutObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	contentType := c.GetHeader("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := h.client.PutObject(c.Request.Context(), key, c.Request.Body, contentType); err != nil {
		log.Printf("[handlers] put error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update cache with metadata from a head call
	meta, err := h.client.HeadObject(c.Request.Context(), key)
	if err != nil {
		// Object was stored but we couldn't read back metadata — update cache with what we know
		log.Printf("[handlers] head after put failed: %v", err)
	} else {
		h.index.Put(key, *meta)
	}

	h.emitEvent("object_uploaded", key)
	c.JSON(http.StatusOK, gin.H{"key": key, "status": "uploaded"})
}

// GetObject handles GET /objects/*key — download an object.
func (h *Handler) GetObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	out, err := h.client.GetObject(c.Request.Context(), key)
	if err != nil {
		log.Printf("[handlers] get error: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer out.Body.Close()

	if out.ContentType != "" {
		c.Header("Content-Type", out.ContentType)
	}
	if out.ETag != "" {
		c.Header("ETag", out.ETag)
	}

	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, out.Body); err != nil {
		log.Printf("[handlers] stream error for %s: %v", key, err)
	}
}

// DeleteObject handles DELETE /objects/*key — move object to trash.
func (h *Handler) DeleteObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if err := h.moveToTrash(c, key); err != nil {
		log.Printf("[handlers] trash error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if strings.HasSuffix(key, "/") {
		h.emitEvent("folder_deleted", key)
	} else {
		h.emitEvent("object_deleted", key)
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "status": "deleted"})
}

// CopyObject handles POST /objects/copy — duplicate an object or folder (server-side copy).
func (h *Handler) CopyObject(c *gin.Context) {
	var req struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Destination == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and destination are required"})
		return
	}

	// Folder copy: list all objects with source prefix, copy each with rewritten key.
	if strings.HasSuffix(req.Source, "/") {
		objects, err := h.client.ListObjects(c.Request.Context(), req.Source)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, obj := range objects {
			destKey := req.Destination + strings.TrimPrefix(obj.Key, req.Source)
			if err := h.client.CopyObject(c.Request.Context(), obj.Key, destKey); err != nil {
				log.Printf("[handlers] copy %s error: %v", obj.Key, err)
				continue
			}
			if meta, err := h.client.HeadObject(c.Request.Context(), destKey); err == nil {
				h.index.Put(destKey, *meta)
			}
		}
		h.emitEvent("folder_copied", fmt.Sprintf("%s -> %s", req.Source, req.Destination))
		c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "copied"})
		return
	}

	if err := h.client.CopyObject(c.Request.Context(), req.Source, req.Destination); err != nil {
		log.Printf("[handlers] copy error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	meta, err := h.client.HeadObject(c.Request.Context(), req.Destination)
	if err == nil {
		h.index.Put(req.Destination, *meta)
	}

	h.emitEvent("object_copied", fmt.Sprintf("%s -> %s", req.Source, req.Destination))
	c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "copied"})
}

// MoveObject handles POST /objects/move — rename/move an object (server-side copy + delete).
func (h *Handler) MoveObject(c *gin.Context) {
	var req struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Destination == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and destination are required"})
		return
	}

	// Folder move: copy all objects with prefix, then delete originals.
	if strings.HasSuffix(req.Source, "/") {
		objects, err := h.client.ListObjects(c.Request.Context(), req.Source)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, obj := range objects {
			destKey := req.Destination + strings.TrimPrefix(obj.Key, req.Source)
			if err := h.client.CopyObject(c.Request.Context(), obj.Key, destKey); err != nil {
				log.Printf("[handlers] move copy %s error: %v", obj.Key, err)
				continue
			}
			if meta, err := h.client.HeadObject(c.Request.Context(), destKey); err == nil {
				h.index.Put(destKey, *meta)
			}
		}
		// Delete originals.
		for _, obj := range objects {
			_ = h.client.DeleteObject(c.Request.Context(), obj.Key)
			h.index.Delete(obj.Key)
		}
		_ = h.client.DeleteObject(c.Request.Context(), req.Source)
		h.index.Delete(req.Source)
		h.emitEvent("folder_moved", fmt.Sprintf("%s -> %s", req.Source, req.Destination))
		c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "moved"})
		return
	}

	if err := h.client.CopyObject(c.Request.Context(), req.Source, req.Destination); err != nil {
		log.Printf("[handlers] move copy error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := h.client.DeleteObject(c.Request.Context(), req.Source); err != nil {
		log.Printf("[handlers] move delete error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	meta, err := h.client.HeadObject(c.Request.Context(), req.Destination)
	if err == nil {
		h.index.Put(req.Destination, *meta)
	}
	h.index.Delete(req.Source)

	h.emitEvent("object_moved", fmt.Sprintf("%s -> %s", req.Source, req.Destination))
	c.JSON(http.StatusOK, gin.H{"source": req.Source, "destination": req.Destination, "status": "moved"})
}

// HeadObject handles HEAD /objects/*key — object metadata.
func (h *Handler) HeadObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	meta, err := h.client.HeadObject(c.Request.Context(), key)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	c.Header("Content-Type", meta.ContentType)
	c.Header("ETag", meta.ETag)
	c.Header("Content-Length", formatInt64(meta.Size))
	c.Header("Last-Modified", meta.LastModified.UTC().Format(http.TimeFormat))
	c.Status(http.StatusOK)
}

// DownloadZip handles GET /objects/zip?prefix= — stream objects as a zip archive.
func (h *Handler) DownloadZip(c *gin.Context) {
	prefix := c.Query("prefix")
	if prefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prefix is required"})
		return
	}

	objects, err := h.client.ListObjects(c.Request.Context(), prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(objects) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no objects found"})
		return
	}

	// Use the folder name for the zip filename.
	folderName := path.Base(strings.TrimSuffix(prefix, "/"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, folderName))

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	for _, obj := range objects {
		rel := strings.TrimPrefix(obj.Key, prefix)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue // skip folder markers
		}
		out, err := h.client.GetObject(c.Request.Context(), obj.Key)
		if err != nil {
			log.Printf("[handlers] zip get %s error: %v", obj.Key, err)
			continue
		}
		w, err := zw.Create(rel)
		if err != nil {
			out.Body.Close()
			continue
		}
		_, _ = io.Copy(w, out.Body)
		out.Body.Close()
	}
}

// Browse handles GET /browse?prefix= — cached directory-like listing.
func (h *Handler) Browse(c *gin.Context) {
	prefix := c.Query("prefix")
	result := h.index.Browse(prefix)
	c.JSON(http.StatusOK, result)
}

// List handles GET /list?prefix= — flat key listing from cache.
func (h *Handler) List(c *gin.Context) {
	prefix := c.Query("prefix")
	objects := h.index.List(prefix)
	c.JSON(http.StatusOK, gin.H{"objects": objects, "count": len(objects)})
}

// Refresh handles POST /refresh — force re-warm index.
func (h *Handler) Refresh(c *gin.Context) {
	if err := h.index.Warm(c.Request.Context()); err != nil {
		log.Printf("[handlers] refresh error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "refreshed", "cached_objects": h.index.Count()})
}

func formatInt64(n int64) string {
	return fmt.Sprintf("%d", n)
}

// --- Tool interface for AI agents -------------------------------------------

// ToolDefs returns the raw tool definition slice for reuse by both the HTTP
// handler and the SDK ToolsFunc registration.
func (h *Handler) ToolDefs() interface{} {
	tools := []gin.H{
		{
			"name":        "list_files",
			"description": "List files and folders at a given path prefix in storage. Returns folder names and file metadata (key, size, content_type, last_modified). Use prefix '' or '/' to list root.",
			"endpoint":    "/mcp/list_files",
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
			"description": "Read a file from storage. Returns the file content as text (for text files) or base64-encoded data (for binary files), along with metadata.",
			"endpoint":    "/mcp/read_file",
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
			"description": "Write or overwrite a file in storage. Creates parent folders automatically. Use this to save code, data, configs, documents, or any content.",
			"endpoint":    "/mcp/write_file",
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
			"description": "Delete a file from storage. The file is moved to trash and can be recovered.",
			"endpoint":    "/mcp/delete_file",
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
			"endpoint":    "/mcp/file_info",
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
			"description": "Create a folder (directory) in storage. Folders are represented as empty marker objects ending with '/'.",
			"endpoint":    "/mcp/create_folder",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"path": gin.H{"type": "string", "description": "Folder path to create, e.g. 'projects/myapp/src/'. A trailing '/' is added if missing."},
				},
				"required": []string{"path"},
			},
		},
		{
			"name":        "browse_trash",
			"description": "Browse deleted files in the trash. Returns folder names and file metadata.",
			"endpoint":    "/mcp/browse_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"prefix": gin.H{"type": "string", "description": "Path prefix within trash to browse. Use empty string for trash root."},
				},
				"required": []string{},
			},
		},
		{
			"name":        "restore_from_trash",
			"description": "Restore a deleted file or folder from trash back to its original location.",
			"endpoint":    "/mcp/restore_from_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"key": gin.H{"type": "string", "description": "Path/key of the file in trash to restore"},
				},
				"required": []string{"key"},
			},
		},
		{
			"name":        "empty_trash",
			"description": "Permanently delete files from trash. Specify a key to delete one item, or omit to empty all trash.",
			"endpoint":    "/mcp/empty_trash",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"key": gin.H{"type": "string", "description": "Optional: specific path/key to permanently delete. Omit to empty all."},
				},
				"required": []string{},
			},
		},
	}
	return tools
}

// Tools returns the tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// ToolListFiles handles POST /mcp/list_files — list files and folders at a prefix.
func (h *Handler) ToolListFiles(c *gin.Context) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	result := h.index.Browse(req.Prefix)
	h.emitEvent("tool_list_files", fmt.Sprintf("prefix=%s folders=%d files=%d", req.Prefix, len(result.Folders), len(result.Files)))
	c.JSON(http.StatusOK, result)
}

// ToolReadFile handles POST /mcp/read_file — read a file and return its content.
func (h *Handler) ToolReadFile(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	out, err := h.client.GetObject(c.Request.Context(), req.Key)
	if err != nil {
		log.Printf("[tool] read error: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
		return
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	h.emitEvent("tool_read_file", fmt.Sprintf("key=%s size=%d", req.Key, len(data)))

	// Determine if content is text-like and return accordingly.
	if isTextContentType(out.ContentType) {
		c.JSON(http.StatusOK, gin.H{
			"key":          req.Key,
			"content":      string(data),
			"content_type": out.ContentType,
			"size":         len(data),
			"encoding":     "text",
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"key":          req.Key,
			"content":      base64.StdEncoding.EncodeToString(data),
			"content_type": out.ContentType,
			"size":         len(data),
			"encoding":     "base64",
		})
	}
}

// ToolWriteFile handles POST /mcp/write_file — write content to a file.
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

	contentType := req.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}

	var body io.Reader
	if req.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid base64 content"})
			return
		}
		body = strings.NewReader(string(decoded))
	} else {
		body = strings.NewReader(req.Content)
	}

	if err := h.client.PutObject(c.Request.Context(), req.Key, body, contentType); err != nil {
		log.Printf("[tool] write error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update index cache.
	meta, err := h.client.HeadObject(c.Request.Context(), req.Key)
	if err == nil {
		h.index.Put(req.Key, *meta)
	}

	h.emitEvent("tool_write_file", fmt.Sprintf("key=%s content_type=%s", req.Key, contentType))
	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "written", "content_type": contentType})
}

// ToolDeleteFile handles POST /mcp/delete_file — move a file to trash.
func (h *Handler) ToolDeleteFile(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if err := h.moveToTrash(c, req.Key); err != nil {
		log.Printf("[tool] trash error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.emitEvent("tool_delete_file", req.Key)
	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "deleted"})
}

// ToolFileInfo handles POST /mcp/file_info — get file metadata.
func (h *Handler) ToolFileInfo(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	meta, err := h.client.HeadObject(c.Request.Context(), req.Key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file not found: %s", req.Key)})
		return
	}

	h.emitEvent("tool_file_info", req.Key)
	c.JSON(http.StatusOK, gin.H{
		"key":           meta.Key,
		"size":          meta.Size,
		"content_type":  meta.ContentType,
		"last_modified": meta.LastModified,
		"etag":          meta.ETag,
	})
}

// ToolCreateFolder handles POST /mcp/create_folder — create a folder marker.
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

	// Create an empty marker object to represent the folder.
	if err := h.client.PutObject(c.Request.Context(), path, strings.NewReader(""), "application/x-directory"); err != nil {
		log.Printf("[tool] create folder error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	meta, err := h.client.HeadObject(c.Request.Context(), path)
	if err == nil {
		h.index.Put(path, *meta)
	}

	h.emitEvent("tool_create_folder", path)
	c.JSON(http.StatusOK, gin.H{"path": path, "status": "created"})
}

// --- Trash endpoints ---

const trashPrefix = ".trash/"

// moveToTrash copies an object to .trash/<key> then deletes the original.
// For folders, all objects under the prefix are trashed.
func (h *Handler) moveToTrash(c *gin.Context, key string) error {
	ctx := c.Request.Context()

	// Folder: trash all objects under this prefix.
	if strings.HasSuffix(key, "/") {
		objects, err := h.client.ListObjects(ctx, key)
		if err != nil {
			return err
		}
		for _, obj := range objects {
			trashKey := trashPrefix + obj.Key
			if err := h.client.CopyObject(ctx, obj.Key, trashKey); err != nil {
				log.Printf("[trash] copy %s error: %v", obj.Key, err)
				continue
			}
			if meta, err := h.client.HeadObject(ctx, trashKey); err == nil {
				h.index.Put(trashKey, *meta)
			}
			if err := h.client.DeleteObject(ctx, obj.Key); err != nil {
				log.Printf("[trash] delete original %s error: %v", obj.Key, err)
				continue
			}
			h.index.Delete(obj.Key)
		}
		// Also trash the folder marker.
		_ = h.client.CopyObject(ctx, key, trashPrefix+key)
		_ = h.client.DeleteObject(ctx, key)
		h.index.Delete(key)
		return nil
	}

	// Single file: copy to trash, verify, then delete.
	trashKey := trashPrefix + key
	if err := h.client.CopyObject(ctx, key, trashKey); err != nil {
		return fmt.Errorf("copy to trash: %w", err)
	}
	if meta, err := h.client.HeadObject(ctx, trashKey); err == nil {
		h.index.Put(trashKey, *meta)
	}
	if err := h.client.DeleteObject(ctx, key); err != nil {
		return fmt.Errorf("delete original after trash: %w", err)
	}
	h.index.Delete(key)
	log.Printf("[trash] trashed %s", key)
	return nil
}

// BrowseTrash handles GET /trash/browse?prefix= — browse trashed objects.
func (h *Handler) BrowseTrash(c *gin.Context) {
	prefix := c.Query("prefix")
	result := h.index.Browse(trashPrefix + prefix)

	// Strip the .trash/ prefix from keys so the UI sees original paths.
	stripped := index.BrowseResult{Prefix: prefix}
	for _, f := range result.Folders {
		stripped.Folders = append(stripped.Folders, strings.TrimPrefix(f, trashPrefix))
	}
	for _, f := range result.Files {
		f.Key = strings.TrimPrefix(f.Key, trashPrefix)
		stripped.Files = append(stripped.Files, f)
	}
	c.JSON(http.StatusOK, stripped)
}

// RestoreTrash handles POST /trash/restore — restore a trashed object.
func (h *Handler) RestoreTrash(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	ctx := c.Request.Context()
	trashKey := trashPrefix + req.Key

	// Check if it's a folder (has objects under .trash/<key>/).
	if strings.HasSuffix(req.Key, "/") {
		objects, err := h.client.ListObjects(ctx, trashKey)
		if err != nil || len(objects) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("not found in trash: %s", req.Key)})
			return
		}
		for _, obj := range objects {
			origKey := strings.TrimPrefix(obj.Key, trashPrefix)
			if err := h.client.CopyObject(ctx, obj.Key, origKey); err != nil {
				log.Printf("[trash] restore copy %s error: %v", obj.Key, err)
				continue
			}
			if meta, err := h.client.HeadObject(ctx, origKey); err == nil {
				h.index.Put(origKey, *meta)
			}
			_ = h.client.DeleteObject(ctx, obj.Key)
			h.index.Delete(obj.Key)
		}
		log.Printf("[trash] restored folder %s", req.Key)
		c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "restored"})
		return
	}

	// Single file restore.
	if _, err := h.client.HeadObject(ctx, trashKey); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("not found in trash: %s", req.Key)})
		return
	}

	if err := h.client.CopyObject(ctx, trashKey, req.Key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta, err := h.client.HeadObject(ctx, req.Key); err == nil {
		h.index.Put(req.Key, *meta)
	}
	if err := h.client.DeleteObject(ctx, trashKey); err != nil {
		log.Printf("[trash] delete trash copy error: %v", err)
	}
	h.index.Delete(trashKey)

	log.Printf("[trash] restored %s", req.Key)
	c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "restored"})
}

// EmptyTrash handles POST /trash/empty — permanently delete trashed objects.
func (h *Handler) EmptyTrash(c *gin.Context) {
	var req struct {
		Key string `json:"key"`
	}
	c.ShouldBindJSON(&req)

	ctx := c.Request.Context()
	prefix := trashPrefix
	if req.Key != "" {
		prefix = trashPrefix + req.Key
	}

	objects, err := h.client.ListObjects(ctx, prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if req.Key != "" && len(objects) == 0 {
		// Single file, not a folder prefix.
		trashKey := trashPrefix + req.Key
		if err := h.client.DeleteObject(ctx, trashKey); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("not found in trash: %s", req.Key)})
			return
		}
		h.index.Delete(trashKey)
		log.Printf("[trash] permanently deleted %s", req.Key)
		c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "permanently_deleted"})
		return
	}

	for _, obj := range objects {
		if err := h.client.DeleteObject(ctx, obj.Key); err != nil {
			log.Printf("[trash] empty delete %s error: %v", obj.Key, err)
			continue
		}
		h.index.Delete(obj.Key)
	}

	if req.Key != "" {
		log.Printf("[trash] permanently deleted %s", req.Key)
		c.JSON(http.StatusOK, gin.H{"key": req.Key, "status": "permanently_deleted"})
	} else {
		log.Printf("[trash] emptied trash (%d objects)", len(objects))
		c.JSON(http.StatusOK, gin.H{"status": "trash_emptied"})
	}
}

// ToolBrowseTrash handles POST /mcp/browse_trash.
func (h *Handler) ToolBrowseTrash(c *gin.Context) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	c.ShouldBindJSON(&req)
	if req.Prefix != "" {
		c.Request.URL.RawQuery = "prefix=" + req.Prefix
	}
	h.BrowseTrash(c)
}

// ToolRestoreFromTrash handles POST /mcp/restore_from_trash.
func (h *Handler) ToolRestoreFromTrash(c *gin.Context) {
	h.RestoreTrash(c)
}

// ToolEmptyTrash handles POST /mcp/empty_trash.
func (h *Handler) ToolEmptyTrash(c *gin.Context) {
	h.EmptyTrash(c)
}

// isTextContentType returns true if the MIME type suggests text content.
func isTextContentType(ct string) bool {
	if ct == "" {
		return true // assume text if unknown
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
