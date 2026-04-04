package pluginsdk

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// SchemaHandler returns an http.HandlerFunc that serves GET /schema.
// Plugins mount this on their own router (e.g. via gin.WrapF) so the
// schema is served on the plugin's main API port — no internal server needed.
//
// Usage with Gin:
//
//	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
func (c *Client) SchemaHandler() http.HandlerFunc {
	reg := &c.registration

	// Dynamic schema — call SchemaFunc on each request.
	if reg.SchemaFunc != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			data := reg.SchemaFunc()
			if reg.ToolsFunc != nil {
				data["tools"] = reg.ToolsFunc()
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		}
	}

	// Static schema — build once.
	schemaData := c.buildSchemaJSON()
	if schemaData == nil && reg.ToolsFunc != nil {
		schemaData = map[string]interface{}{}
	}
	if schemaData != nil {
		if reg.ToolsFunc != nil {
			return func(w http.ResponseWriter, r *http.Request) {
				data := c.buildSchemaJSON()
				if data == nil {
					data = map[string]interface{}{}
				}
				data["tools"] = reg.ToolsFunc()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(data)
			}
		}
		schemaBytes, err := json.Marshal(schemaData)
		if err != nil {
			log.Printf("pluginsdk: WARNING: failed to marshal schema: %v", err)
			return func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "schema marshal error", http.StatusInternalServerError)
			}
		}
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(schemaBytes)
		}
	}

	// No schema at all — 404.
	return func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}
}

// EventHandler returns an http.HandlerFunc that receives POST /events
// callbacks. Handles both application events (from event bus push delivery)
// and lifecycle events (from kernel direct broadcast: plugin:ready,
// plugin:stopped, plugin:registry-sync).
//
// Usage with Gin:
//
//	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))
func (c *Client) EventHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var event EventCallback
		if err := json.Unmarshal(body, &event); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// Handle kernel lifecycle events (peer registry updates).
		if c.handleLifecycleEvent(event) {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Dispatch to application event handlers.
		c.eventMu.RLock()
		debouncer, ok := c.eventDebouncers[event.EventType]
		c.eventMu.RUnlock()

		if ok {
			debouncer.Submit(event)
		} else {
			log.Printf("pluginsdk: no handler for event type %q", event.EventType)
		}

		w.WriteHeader(http.StatusOK)
	}
}

// handleLifecycleEvent processes kernel lifecycle events embedded in the
// standard EventCallback format. Returns true if the event was a lifecycle
// event (handled internally), false otherwise.
func (c *Client) handleLifecycleEvent(event EventCallback) bool {
	switch event.EventType {
	case "plugin:ready", "plugin:stopped", "plugin:started", "plugin:unhealthy", "plugin:healthy", "plugin:registry-sync":
		// Detail contains the original lifecycle payload as JSON.
	default:
		return false
	}

	var payload struct {
		Type   string `json:"type"`
		Plugin string `json:"plugin_id,omitempty"`
		Host   string `json:"host,omitempty"`
		Port   int    `json:"http_port,omitempty"`
		// For registry-sync: bulk address map.
		Registry []struct {
			ID       string `json:"id"`
			Host     string `json:"host"`
			HTTPPort int    `json:"http_port"`
		} `json:"registry,omitempty"`
	}
	if err := json.Unmarshal([]byte(event.Detail), &payload); err != nil {
		log.Printf("pluginsdk: bad lifecycle detail: %v", err)
		return true
	}

	switch payload.Type {
	case "plugin:ready", "plugin:healthy":
		if payload.Plugin != "" && payload.Host != "" {
			c.SetPeer(payload.Plugin, payload.Host, payload.Port)
		}
	case "plugin:stopped", "plugin:unhealthy":
		if payload.Plugin != "" {
			c.invalidatePeer(payload.Plugin)
		}
	case "plugin:started":
		// Container booting — invalidate stale address, plugin:ready will set the new one.
		if payload.Plugin != "" {
			c.invalidatePeer(payload.Plugin)
		}
	case "plugin:registry-sync":
		c.peersMu.Lock()
		c.peers = make(map[string]peerEntry, len(payload.Registry))
		for _, p := range payload.Registry {
			if p.Host != "" {
				c.peers[p.ID] = peerEntry{Host: p.Host, HTTPPort: p.HTTPPort}
			}
		}
		c.peersMu.Unlock()
		log.Printf("pluginsdk: registry-sync received, %d peers", len(payload.Registry))
	}

	return true
}
