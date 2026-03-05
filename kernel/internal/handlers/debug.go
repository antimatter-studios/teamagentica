package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// DebugEventsSSE handles GET /api/debug/events — streams debug events via SSE.
func DebugEventsSSE(hub *events.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		c.Writer.Flush()

		ch := hub.Subscribe()
		defer hub.Unsubscribe(ch)

		// Send keepalive comments every 15s to prevent proxy timeout.
		keepalive := time.NewTicker(15 * time.Second)
		defer keepalive.Stop()

		ctx := c.Request.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepalive.C:
				fmt.Fprintf(c.Writer, ": keepalive\n\n")
				if f, ok := c.Writer.(http.Flusher); ok {
					f.Flush()
				}
			case msg, ok := <-ch:
				if !ok {
					return
				}
				raw, err := events.MarshalSSEMessage(msg)
				if err != nil {
					continue
				}
				c.Writer.Write(raw)
				if f, ok := c.Writer.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}
}

// DebugEventsHistory handles GET /api/debug/history — returns recent event log.
// Each entry has {channel, data} shape so the frontend can route by type.
func DebugEventsHistory(hub *events.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 200
		if l := c.Query("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 {
				limit = v
			}
		}
		c.JSON(http.StatusOK, gin.H{"events": hub.History(limit)})
	}
}

// DebugEventLog handles GET /api/debug/event-log — returns persistent inter-plugin
// event log entries. Unlike /history (in-memory ring buffer), these survive restarts
// and are never automatically deleted.
func DebugEventLog(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 100
		if l := c.Query("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
				limit = v
			}
		}

		var logs []models.EventLog
		db.Order("created_at DESC").Limit(limit).Find(&logs)
		c.JSON(http.StatusOK, gin.H{"events": logs})
	}
}

// DebugEventsTest handles GET /api/debug/test — emits a test event.
func DebugEventsTest(hub *events.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		hub.Emit(events.DebugEvent{
			Type:     "test",
			PluginID: "kernel",
			Detail:   "debug console connected",
		})
		c.JSON(http.StatusOK, gin.H{"message": "test event emitted"})
	}
}
