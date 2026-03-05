package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/index"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/s3client"
)

type Handler struct {
	cfg    *config.Config
	client *s3client.Client
	index  *index.Index
	sdk    *pluginsdk.Client
}

func NewHandler(cfg *config.Config, client *s3client.Client, idx *index.Index) *Handler {
	return &Handler{cfg: cfg, client: client, index: idx}
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

// DeleteObject handles DELETE /objects/*key — delete an object.
func (h *Handler) DeleteObject(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if err := h.client.DeleteObject(c.Request.Context(), key); err != nil {
		log.Printf("[handlers] delete error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.index.Delete(key)
	h.emitEvent("object_deleted", key)
	c.JSON(http.StatusOK, gin.H{"key": key, "status": "deleted"})
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
