// Package handlers implements the REST event API backed by Redis Streams.
// The API is transport-agnostic — no Redis terminology is exposed to callers.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// PeerResolver resolves a plugin ID to its event callback URL using the
// SDK peer registry cache. Returns ("", false) if the plugin is unknown.
type PeerResolver interface {
	PeerEventURL(pluginID string) (string, bool)
}

// EventHandler exposes REST endpoints for the platform event system.
// Internally it uses Redis Streams for persistence and fan-out via push.
type EventHandler struct {
	rdb      *redis.Client
	peers    PeerResolver
	debug    bool
	pushHTTP *http.Client
}

// NewEventHandler creates a new handler backed by the given Redis client.
// The PeerResolver is used to look up subscriber event URLs for push delivery.
func NewEventHandler(rdb *redis.Client, peers PeerResolver, debug bool) *EventHandler {
	return &EventHandler{
		rdb:   rdb,
		peers: peers,
		debug: debug,
		pushHTTP: &http.Client{
			Timeout: 3 * time.Second,
		},
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
// event type. Resolves each subscriber's event URL via the peer registry cache.
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

		url, ok := h.peers.PeerEventURL(pluginID)
		if !ok {
			if h.debug {
				log.Printf("events: fanout skip %s (no peer address)", pluginID)
			}
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := h.pushHTTP.Do(req)
		if err != nil {
			if h.debug {
				log.Printf("events: fanout to %s failed: %v", pluginID, err)
			}
			continue
		}
		resp.Body.Close()
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
