// Package handlers implements the REST event API backed by Redis Streams.
// The API is transport-agnostic — no Redis terminology is exposed to callers.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// PluginRouter routes requests to other plugins via the SDK P2P mechanism.
type PluginRouter interface {
	RouteToPlugin(ctx context.Context, pluginID, method, path string, body io.Reader) ([]byte, error)
}

// EventHandler exposes REST endpoints for the platform event system.
// Internally it uses Redis Streams for persistence and fan-out via push.
type EventHandler struct {
	rdb    *redis.Client
	router PluginRouter
	debug  bool
}

// NewEventHandler creates a new handler backed by the given Redis client.
// The PluginRouter is used to deliver events to subscribers via RouteToPlugin.
func NewEventHandler(rdb *redis.Client, router PluginRouter, debug bool) *EventHandler {
	return &EventHandler{
		rdb:    rdb,
		router: router,
		debug:  debug,
	}
}

// subscriberKey returns the Redis set key that tracks subscribers for an event type.
func subscriberKey(eventType string) string {
	return "subscribers:" + eventType
}

// streamKey returns the Redis Stream key for a given event type.
// Events are partitioned by type so consumers can subscribe selectively.
func streamKey(eventType string) string {
	return "events:" + eventType
}

// consumerGroup returns the consumer group name for a given plugin.
// Each plugin gets its own consumer group so it reads independently.
func consumerGroup(pluginID string) string {
	return "cg:" + pluginID
}

// --- REST Endpoints ---

// PublishRequest is the body for POST /events/publish.
type PublishRequest struct {
	EventType string `json:"event_type" binding:"required"`
	Source     string `json:"source" binding:"required"`
	Target     string `json:"target,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Publish handles POST /events/publish — adds an event to the stream.
func (h *EventHandler) Publish(c *gin.Context) {
	var req PublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	key := streamKey(req.EventType)

	fields := map[string]interface{}{
		"source": req.Source,
		"detail": req.Detail,
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if req.Target != "" {
		fields["target"] = req.Target
	}

	// XADD with MAXLEN ~ 10000 to cap stream growth.
	id, err := h.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: 10000,
		Approx: true,
		Values: fields,
	}).Result()
	if err != nil {
		log.Printf("events: publish failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "publish failed"})
		return
	}

	if h.debug {
		log.Printf("events: published %s from %s (id=%s)", req.EventType, req.Source, id)
	}

	// Also publish to a global stream for broadcast subscribers.
	h.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "events:_all",
		MaxLen: 50000,
		Approx: true,
		Values: map[string]interface{}{
			"event_type": req.EventType,
			"source":     req.Source,
			"target":     req.Target,
			"detail":     req.Detail,
			"ts":         fields["ts"],
		},
	})

	// Fan out: push event to all subscribers via their event ports.
	go h.fanOutToSubscribers(req.EventType, req.Source, req.Detail, fields["ts"].(string))

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// fanOutToSubscribers pushes an event to all plugins subscribed to the given
// event type. Uses RouteToPlugin for standard SDK P2P delivery.
func (h *EventHandler) fanOutToSubscribers(eventType, source, detail, ts string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subscribers, err := h.rdb.SMembers(ctx, subscriberKey(eventType)).Result()
	if err != nil || len(subscribers) == 0 {
		return
	}

	payload, err := json.Marshal(map[string]string{
		"event_type": eventType,
		"plugin_id":  source,
		"detail":     detail,
		"timestamp":  ts,
	})
	if err != nil {
		return
	}

	for _, pluginID := range subscribers {
		// Don't push events back to the source plugin.
		if pluginID == source {
			continue
		}

		_, err := h.router.RouteToPlugin(ctx, pluginID, "POST", "/events", bytes.NewReader(payload))
		if err != nil {
			if h.debug {
				log.Printf("events: fanout to %s failed: %v", pluginID, err)
			}
			continue
		}
	}
}

// SubscribeRequest is the body for POST /events/subscribe.
type SubscribeRequest struct {
	PluginID  string `json:"plugin_id" binding:"required"`
	EventType string `json:"event_type" binding:"required"`
}

// Subscribe handles POST /events/subscribe — registers a plugin as a subscriber.
// The plugin's event port will receive pushed events when they are published.
func (h *EventHandler) Subscribe(c *gin.Context) {
	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Add plugin to the subscriber set for this event type.
	if err := h.rdb.SAdd(ctx, subscriberKey(req.EventType), req.PluginID).Err(); err != nil {
		log.Printf("events: subscribe failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "subscribe failed"})
		return
	}

	if h.debug {
		log.Printf("events: %s subscribed to %s", req.PluginID, req.EventType)
	}

	c.JSON(http.StatusOK, gin.H{"status": "subscribed"})
}

// Unsubscribe handles DELETE /events/subscribe/:plugin_id/:event_type.
func (h *EventHandler) Unsubscribe(c *gin.Context) {
	pluginID := c.Param("plugin_id")
	eventType := c.Param("event_type")

	ctx := c.Request.Context()

	// Remove plugin from the subscriber set.
	h.rdb.SRem(ctx, subscriberKey(eventType), pluginID)

	if h.debug {
		log.Printf("events: %s unsubscribed from %s", pluginID, eventType)
	}

	c.JSON(http.StatusOK, gin.H{"status": "unsubscribed"})
}

// EventMessage is a single event returned to consumers.
type EventMessage struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Source    string `json:"source"`
	Target    string `json:"target,omitempty"`
	Detail   string `json:"detail"`
	Timestamp string `json:"timestamp"`
}

// Consume handles GET /events/consume/:plugin_id/:event_type — reads pending events.
// Returns immediately with available events (no long-polling — keep it simple).
func (h *EventHandler) Consume(c *gin.Context) {
	pluginID := c.Param("plugin_id")
	eventType := c.Param("event_type")
	countStr := c.DefaultQuery("count", "100")
	count, _ := strconv.ParseInt(countStr, 10, 64)
	if count <= 0 || count > 1000 {
		count = 100
	}

	ctx := c.Request.Context()
	key := streamKey(eventType)
	group := consumerGroup(pluginID)

	// Read new messages for this consumer group.
	// Use a short block timeout so the call returns quickly when empty.
	streams, err := h.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: pluginID,
		Streams:  []string{key, ">"},
		Count:    count,
		Block:    100 * time.Millisecond,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			c.JSON(http.StatusOK, gin.H{"events": []EventMessage{}})
			return
		}
		log.Printf("events: consume failed for %s/%s: %v", pluginID, eventType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "consume failed"})
		return
	}

	messages := make([]EventMessage, 0)
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			em := EventMessage{
				ID:        msg.ID,
				EventType: eventType,
				Source:    getString(msg.Values, "source"),
				Target:    getString(msg.Values, "target"),
				Detail:   getString(msg.Values, "detail"),
				Timestamp: getString(msg.Values, "ts"),
			}
			messages = append(messages, em)

			// ACK immediately — at-most-once delivery for simplicity.
			h.rdb.XAck(ctx, key, group, msg.ID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"events": messages})
}

// History handles GET /events/history — queries past events from a stream.
func (h *EventHandler) History(c *gin.Context) {
	eventType := c.DefaultQuery("type", "_all")
	countStr := c.DefaultQuery("count", "50")
	count, _ := strconv.ParseInt(countStr, 10, 64)
	if count <= 0 || count > 1000 {
		count = 50
	}

	ctx := c.Request.Context()
	key := streamKey(eventType)

	// XREVRANGE — newest first.
	messages, err := h.rdb.XRevRange(ctx, key, "+", "-").Result()
	if err != nil {
		log.Printf("events: history failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "history query failed"})
		return
	}

	// Limit results.
	if int64(len(messages)) > count {
		messages = messages[:count]
	}

	result := make([]EventMessage, len(messages))
	for i, msg := range messages {
		et := eventType
		if et == "_all" {
			et = getString(msg.Values, "event_type")
		}
		result[i] = EventMessage{
			ID:        msg.ID,
			EventType: et,
			Source:    getString(msg.Values, "source"),
			Target:    getString(msg.Values, "target"),
			Detail:   getString(msg.Values, "detail"),
			Timestamp: getString(msg.Values, "ts"),
		}
	}

	c.JSON(http.StatusOK, gin.H{"events": result, "count": len(result)})
}

// Stats handles GET /events/stats — returns stream info for monitoring.
func (h *EventHandler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	// Scan for all event streams.
	var cursor uint64
	var streams []string
	for {
		keys, next, err := h.rdb.Scan(ctx, cursor, "events:*", 100).Result()
		if err != nil {
			break
		}
		streams = append(streams, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	stats := make(map[string]interface{})
	for _, key := range streams {
		length, err := h.rdb.XLen(ctx, key).Result()
		if err != nil {
			continue
		}
		// Strip "events:" prefix for cleaner output.
		name := key[7:]
		stats[name] = map[string]interface{}{
			"length": length,
		}
	}

	c.JSON(http.StatusOK, gin.H{"streams": stats})
}

// Stream handles GET /events/stream — SSE endpoint that streams all events
// from the events:_all Redis Stream in real time. The dashboard connects here
// instead of the kernel's SSE hub, making infra-redis the single event source.
func (h *EventHandler) Stream(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	ctx := c.Request.Context()

	// Start reading from the latest entry ($ = only new messages).
	lastID := "$"

	// Send recent history first so the client has context.
	recent, err := h.rdb.XRevRange(ctx, "events:_all", "+", "-").Result()
	if err == nil && len(recent) > 0 {
		// Send up to 200 recent events, oldest first.
		limit := 200
		if len(recent) < limit {
			limit = len(recent)
		}
		for i := limit - 1; i >= 0; i-- {
			msg := recent[i]
			h.writeSSEMessage(c.Writer, msg.Values)
		}
		c.Writer.Flush()
		// Start streaming from after the newest message we sent.
		lastID = recent[0].ID
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Keepalive comment.
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		default:
			// Poll for new messages with a short block.
			streams, err := h.rdb.XRead(ctx, &redis.XReadArgs{
				Streams: []string{"events:_all", lastID},
				Count:   100,
				Block:   2 * time.Second,
			}).Result()
			if err != nil {
				if err == redis.Nil {
					continue // No new messages, loop back.
				}
				if ctx.Err() != nil {
					return // Client disconnected.
				}
				continue
			}

			for _, stream := range streams {
				for _, msg := range stream.Messages {
					h.writeSSEMessage(c.Writer, msg.Values)
					lastID = msg.ID
				}
			}
			c.Writer.Flush()
		}
	}
}

// writeSSEMessage formats a Redis stream entry as an SSE event.
func (h *EventHandler) writeSSEMessage(w gin.ResponseWriter, values map[string]interface{}) {
	data, err := json.Marshal(EventMessage{
		EventType: getString(values, "event_type"),
		Source:    getString(values, "source"),
		Target:    getString(values, "target"),
		Detail:    getString(values, "detail"),
		Timestamp: getString(values, "ts"),
	})
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: event\ndata: %s\n\n", data)
}

// EnsureStream pre-creates a stream so consumer groups can be created on it.
func (h *EventHandler) EnsureStream(ctx context.Context, eventType string) error {
	key := streamKey(eventType)
	// XADD with MAXLEN 0 doesn't add anything but creates the stream.
	// Instead, add a sentinel and immediately trim.
	_, err := h.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]interface{}{"_init": "1"},
		MaxLen: 1,
	}).Result()
	return err
}

// getString safely extracts a string from Redis stream values.
func getString(values map[string]interface{}, key string) string {
	v, ok := values[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}
