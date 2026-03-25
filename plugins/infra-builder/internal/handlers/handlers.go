package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

const maxBuildHistory = 50

// BuildRecord stores metadata about a completed or in-progress build.
type BuildRecord struct {
	ID         string `json:"id"`
	Image      string `json:"image"`
	Tag        string `json:"tag"`
	Volume     string `json:"volume"`
	Dockerfile string `json:"dockerfile"`
	Status     string `json:"status"` // "building", "success", "failed"
	StartedAt  string `json:"started_at"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	Logs       string `json:"-"` // stored separately, not in list response
}

type Handler struct {
	sdk   *pluginsdk.Client
	debug bool

	mu       sync.Mutex
	builds   []BuildRecord
	building bool // only one build at a time
}

func NewHandler(sdk *pluginsdk.Client, debug bool) *Handler {
	return &Handler{
		sdk:   sdk,
		debug: debug,
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"plugin":  "infra-builder",
		"version": "1.0.0",
	})
}

func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "build",
			"description": "Build a Docker image from source in a storage volume. Streams build output as NDJSON. Returns the built image name and tag.",
			"endpoint":    "/mcp/build",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"volume":     gin.H{"type": "string", "description": "Storage volume name containing the source code"},
					"dockerfile": gin.H{"type": "string", "description": "Path to Dockerfile relative to volume root (default: 'Dockerfile')"},
					"image":      gin.H{"type": "string", "description": "Image name (e.g. 'teamagentica-messaging-discord')"},
					"tag":        gin.H{"type": "string", "description": "Image tag (default: timestamp-based)"},
				},
				"required": []string{"volume", "image"},
			},
		},
	}
}

func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

func (h *Handler) ToolBuild(c *gin.Context) {
	h.Build(c)
}

func (h *Handler) ListBuilds(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.builds == nil {
		c.JSON(http.StatusOK, gin.H{"builds": []BuildRecord{}})
		return
	}

	// Return without logs.
	c.JSON(http.StatusOK, gin.H{"builds": h.builds})
}

func (h *Handler) GetBuildLogs(c *gin.Context) {
	id := c.Param("id")

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, b := range h.builds {
		if b.ID == id {
			c.JSON(http.StatusOK, gin.H{"id": b.ID, "logs": b.Logs})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "build not found"})
}

func (h *Handler) addBuild(b BuildRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.builds = append(h.builds, b)
	if len(h.builds) > maxBuildHistory {
		h.builds = h.builds[len(h.builds)-maxBuildHistory:]
	}
}

func (h *Handler) updateBuild(id string, fn func(*BuildRecord)) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := range h.builds {
		if h.builds[i].ID == id {
			fn(&h.builds[i])
			return
		}
	}
}

func generateBuildID() string {
	return time.Now().Format("20060102-150405")
}
