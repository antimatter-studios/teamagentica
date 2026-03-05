package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/scheduler/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/scheduler/internal/scheduler"
)

type Handler struct {
	cfg       *config.Config
	scheduler *scheduler.Scheduler
	sdk       *pluginsdk.Client
}

func NewHandler(cfg *config.Config, sched *scheduler.Scheduler) *Handler {
	return &Handler{cfg: cfg, scheduler: sched}
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
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- Event CRUD ---

type createEventRequest struct {
	Name     string `json:"name" binding:"required"`
	Text     string `json:"text" binding:"required"`
	Type     string `json:"type" binding:"required"` // "once" or "repeat"
	Interval string `json:"interval" binding:"required"` // Go duration string: "30s", "5m", "1h", "24h"
}

// CreateEvent handles POST /events.
func (h *Handler) CreateEvent(c *gin.Context) {
	var req createEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	eventType := scheduler.EventType(req.Type)
	if eventType != scheduler.Once && eventType != scheduler.Repeat {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be 'once' or 'repeat'"})
		return
	}

	interval, err := time.ParseDuration(req.Interval)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid interval: " + err.Error()})
		return
	}
	if interval < 1*time.Second {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval must be at least 1s"})
		return
	}

	event := scheduler.CreateEvent(req.Name, req.Text, eventType, interval)
	h.scheduler.Add(event)

	h.emitEvent("event_created", event.Name+" ("+req.Interval+")")
	c.JSON(http.StatusCreated, event)
}

// ListEvents handles GET /events.
func (h *Handler) ListEvents(c *gin.Context) {
	events := h.scheduler.List()
	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

// GetEvent handles GET /events/:id.
func (h *Handler) GetEvent(c *gin.Context) {
	id := c.Param("id")
	event, ok := h.scheduler.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	c.JSON(http.StatusOK, event)
}

type updateEventRequest struct {
	Name     *string `json:"name"`
	Text     *string `json:"text"`
	Interval *string `json:"interval"`
	Enabled  *bool   `json:"enabled"`
}

// UpdateEvent handles PUT /events/:id.
func (h *Handler) UpdateEvent(c *gin.Context) {
	id := c.Param("id")

	var req updateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var interval *time.Duration
	if req.Interval != nil {
		d, err := time.ParseDuration(*req.Interval)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid interval: " + err.Error()})
			return
		}
		if d < 1*time.Second {
			c.JSON(http.StatusBadRequest, gin.H{"error": "interval must be at least 1s"})
			return
		}
		interval = &d
	}

	event, err := h.scheduler.Update(id, req.Name, req.Text, interval, req.Enabled)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	h.emitEvent("event_updated", event.Name)
	c.JSON(http.StatusOK, event)
}

// DeleteEvent handles DELETE /events/:id.
func (h *Handler) DeleteEvent(c *gin.Context) {
	id := c.Param("id")
	if !h.scheduler.Delete(id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	h.emitEvent("event_deleted", id)
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// --- Event Log ---

// GetLog handles GET /log.
func (h *Handler) GetLog(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	entries := h.scheduler.Logs(limit)
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}
