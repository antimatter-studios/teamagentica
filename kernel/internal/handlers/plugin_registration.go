package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/watchdog"
)

// containerNameRe matches valid Docker container names (alphanumeric, hyphens, underscores, dots).
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validatePluginHost checks that the host provided by a self-registering plugin
// is not a loopback, link-local, or metadata address. Docker network IPs
// (10.0.0.0/8, 172.16.0.0/12) and container-name hostnames are allowed.
func validatePluginHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is required")
	}

	lower := strings.ToLower(strings.TrimSpace(host))

	// Reject localhost explicitly.
	if lower == "localhost" {
		return fmt.Errorf("host %q is not allowed: localhost rejected", host)
	}

	// If it looks like a container name (not an IP), allow it.
	if net.ParseIP(host) == nil && containerNameRe.MatchString(host) {
		return nil
	}

	// Resolve hostname to IPs.
	ips, err := net.LookupHost(host)
	if err != nil {
		// If we can't resolve and it's not a valid container name, reject.
		return fmt.Errorf("host %q cannot be resolved: %v", host, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}

		// Reject loopback: 127.0.0.0/8, ::1
		if ip.IsLoopback() {
			return fmt.Errorf("host %q resolves to loopback address %s", host, ipStr)
		}

		// Reject link-local: 169.254.0.0/16 (includes cloud metadata endpoints)
		if ip.IsLinkLocalUnicast() {
			return fmt.Errorf("host %q resolves to link-local address %s", host, ipStr)
		}
	}

	return nil
}

// proxyClient returns an http.Client that reuses the shared transport.
func (h *PluginHandler) proxyClient() *http.Client {
	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: h.transport,
	}
}

// pluginScheme returns "https" if mTLS is enabled, "http" otherwise.
func (h *PluginHandler) pluginScheme() string {
	if h.clientTLS != nil {
		return "https"
	}
	return "http"
}

// --- request types for plugin self-registration ---

type pluginSelfRegisterRequest struct {
	ID           string                 `json:"id" binding:"required"`
	Name         string                 `json:"name"`
	Host         string                 `json:"host" binding:"required"`
	Port         int                    `json:"port"`
	EventPort    int                    `json:"event_port,omitempty"`
	Capabilities []string               `json:"capabilities"`
	Dependencies *pluginDependencies    `json:"dependencies,omitempty"`
	Version      string                 `json:"version"`
	Candidate    bool                   `json:"candidate,omitempty"` // true if this is a candidate container
	ConfigSchema    map[string]interface{} `json:"config_schema,omitempty"`
	WorkspaceSchema map[string]interface{} `json:"workspace_schema,omitempty"`
}

type pluginDependencies struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

type pluginHeartbeatRequest struct {
	ID        string `json:"id" binding:"required"`
	Candidate bool   `json:"candidate,omitempty"`
}

type pluginDeregisterRequest struct {
	ID string `json:"id" binding:"required"`
}

// --- handlers ---

// SelfRegister handles POST /api/plugins/register — called by plugins via the SDK.
// The plugin must already be installed (exist in the DB). This updates host/port/status.
func (h *PluginHandler) SelfRegister(c *gin.Context) {
	var req pluginSelfRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the plugin's host to prevent SSRF via host spoofing.
	// Blocks loopback (127.x), link-local/metadata (169.254.x), and ::1.
	// Allows Docker network IPs and container-name hostnames.
	if err := validatePluginHost(req.Host); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid host: %v", err)})
		return
	}

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", req.ID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found in registry", req.ID)})
		return
	}

	now := time.Now()

	// Candidate registration — update candidate fields only.
	if req.Candidate {
		updates := map[string]interface{}{
			"candidate_host":        req.Host,
			"candidate_port":        req.Port,
			"candidate_event_port":  req.EventPort,
			"candidate_healthy":     true,
			"candidate_last_seen":   now,
		}
		if req.Version != "" {
			updates["candidate_version"] = req.Version
		}
		h.db().Model(&plugin).Updates(updates)

		h.Events.Emit(events.DebugEvent{
			Type:     "register",
			PluginID: req.ID,
			Detail:   fmt.Sprintf("candidate host=%s port=%d", req.Host, req.Port),
		})

		c.JSON(http.StatusOK, gin.H{"message": "registered as candidate"})
		return
	}

	// Primary registration.
	updates := map[string]interface{}{
		"host":       req.Host,
		"http_port":  req.Port,
		"event_port": req.EventPort,
		"status":     "running",
		"last_seen":  now,
	}

	if req.Version != "" {
		updates["version"] = req.Version
	}

	if req.Name != "" {
		updates["name"] = stripHTMLTags(req.Name)
	}

	if req.Capabilities != nil {
		plugin.SetCapabilities(req.Capabilities)
		updates["capabilities"] = plugin.Capabilities
	}

	if req.Dependencies != nil && len(req.Dependencies.Capabilities) > 0 {
		plugin.SetDependencies(req.Dependencies.Capabilities)
		updates["dependencies"] = plugin.Dependencies

		// Auto-enable dependency plugins when a plugin registers with deps.
		go func() {
			var allEnabled []string
			visited := map[string]bool{req.ID: true}
			for _, cap := range req.Dependencies.Capabilities {
				var allPlugins []models.Plugin
				h.db().Find(&allPlugins)
				for i := range allPlugins {
					for _, c := range allPlugins[i].GetCapabilities() {
						if c == cap {
							if err := h.enablePlugin(context.Background(), &allPlugins[i], visited, &allEnabled); err != nil {
								log.Printf("plugins: auto-enable dep %s for %s failed: %v", allPlugins[i].ID, req.ID, err)
							}
							break
						}
					}
				}
			}
		}()
	}

	// Cache schema sent at registration time so config/schema endpoints work
	// even when the plugin is temporarily unreachable. The plugin always pushes
	// its current schema on startup, keeping the cached version in sync.
	if req.ConfigSchema != nil {
		if data, err := json.Marshal(req.ConfigSchema); err == nil {
			updates["config_schema"] = models.JSONRawString(data)
		}
	}
	if req.WorkspaceSchema != nil {
		if data, err := json.Marshal(req.WorkspaceSchema); err == nil {
			updates["workspace_schema"] = models.JSONRawString(data)
		}
	}

	h.db().Model(&plugin).Updates(updates)

	h.Events.Emit(events.DebugEvent{
		Type:     "register",
		PluginID: req.ID,
		Detail:   fmt.Sprintf("host=%s port=%d", req.Host, req.Port),
	})

	// Dispatch plugin:registered as an inter-plugin event so other plugins
	// can react to new capabilities coming online (e.g. ngrok re-emitting
	// its tunnel URL when webhook:ingress appears).
	if subs := h.Subs.GetSubscribers("plugin:registered"); len(subs) > 0 {
		capsJSON, _ := json.Marshal(req.Capabilities)
		detail := fmt.Sprintf(`{"plugin_id":%q,"capabilities":%s}`, req.ID, string(capsJSON))
		payload := map[string]string{
			"event_type": "plugin:registered",
			"plugin_id":  req.ID,
			"detail":     detail,
			"timestamp":  time.Now().Format(time.RFC3339),
		}
		body, _ := json.Marshal(payload)
		for _, sub := range subs {
			h.Events.Emit(events.DebugEvent{
				Type:     "dispatch",
				PluginID: sub.PluginID,
				Detail:   fmt.Sprintf("event=plugin:registered from=%s callback=%s", req.ID, sub.CallbackPath),
			})
			h.logEvent("plugin:registered", req.ID, sub.PluginID, "dispatched", fmt.Sprintf("callback=%s", sub.CallbackPath))
			go h.dispatchEvent(sub, body)
		}
	}

	// Flush any pending addressed events for this plugin.
	go h.flushPendingEvents(req.ID)

	c.JSON(http.StatusOK, gin.H{"message": "registered"})
}

// Heartbeat handles POST /api/plugins/heartbeat — called periodically by plugins.
func (h *PluginHandler) Heartbeat(c *gin.Context) {
	var req pluginHeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", req.ID); result.Error != nil {
		database.CheckError(result.Error)
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", req.ID)})
		return
	}

	if req.Candidate {
		h.db().Model(&plugin).Updates(map[string]interface{}{
			"candidate_last_seen": time.Now(),
			"candidate_healthy":   true,
		})
	} else {
		h.db().Model(&plugin).Updates(map[string]interface{}{
			"last_seen": time.Now(),
			"status":    "running",
		})
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "heartbeat",
		PluginID: req.ID,
	})

	// If host/port is empty, tell the plugin to re-register so the kernel
	// can recover the connection without restarting the container.
	msg := watchdog.HeartbeatStatus(&plugin)
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

// Deregister handles POST /api/plugins/deregister — called by plugins on shutdown.
func (h *PluginHandler) Deregister(c *gin.Context) {
	var req pluginDeregisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", req.ID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", req.ID)})
		return
	}

	h.db().Model(&plugin).Updates(map[string]interface{}{
		"status": "stopped",
		"host":   "",
	})

	// Remove all event subscriptions for this plugin.
	h.Subs.UnsubscribeAll(req.ID)

	h.Events.Emit(events.DebugEvent{
		Type:     "deregister",
		PluginID: req.ID,
	})

	c.JSON(http.StatusOK, gin.H{"message": "deregistered"})
}

// ReportEvent handles POST /api/plugins/event — allows plugins to emit debug events.
func (h *PluginHandler) ReportEvent(c *gin.Context) {
	var req struct {
		ID          string `json:"id" binding:"required"`
		Type        string `json:"type" binding:"required"`
		Detail      string `json:"detail"`
		Destination string `json:"destination"` // optional: addressed delivery target
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()

	// Always emit to SSE debug console for observability.
	h.Events.Emit(events.DebugEvent{
		Type:     req.Type,
		PluginID: req.ID,
		Detail:   req.Detail,
	})

	if req.Destination != "" {
		// Addressed delivery: guarantee at-least-once to the specific destination.
		h.handleAddressedEvent(req.ID, req.Type, req.Detail, req.Destination, now)
	} else {
		// Fire-and-forget broadcast to all subscribers.
		if subs := h.Subs.GetSubscribers(req.Type); len(subs) > 0 {
			payload := map[string]string{
				"event_type": req.Type,
				"plugin_id":  req.ID,
				"detail":     req.Detail,
				"timestamp":  now.Format(time.RFC3339),
			}
			body, _ := json.Marshal(payload)

			for _, sub := range subs {
				h.Events.Emit(events.DebugEvent{
					Type:     "dispatch",
					PluginID: sub.PluginID,
					Detail:   fmt.Sprintf("event=%s from=%s callback=%s", req.Type, req.ID, sub.CallbackPath),
				})
				h.logEvent(req.Type, req.ID, sub.PluginID, "dispatched", fmt.Sprintf("callback=%s", sub.CallbackPath))
				go h.dispatchEvent(sub, body)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// handleAddressedEvent guarantees at-least-once delivery to a specific plugin.
func (h *PluginHandler) handleAddressedEvent(sourceID, eventType, detail, destID string, ts time.Time) {
	// Find the subscription for (destination, event_type) to get callback_path.
	sub, found := h.Subs.FindSubscription(destID, eventType)

	// Determine callback path — use subscription if found, otherwise default.
	callbackPath := "/events/usage"
	if found {
		callbackPath = sub.CallbackPath
	}

	payload := map[string]string{
		"event_type": eventType,
		"plugin_id":  sourceID,
		"detail":     detail,
		"timestamp":  ts.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)

	// Persist to pending_events immediately.
	pending := models.Event{
		EventType:      eventType,
		SourcePluginID: sourceID,
		TargetPluginID: destID,
		CallbackPath:   callbackPath,
		Payload:        string(body),
		Attempts:       0,
		CreatedAt:      ts,
	}
	if err := h.db().Create(&pending).Error; err != nil {
		log.Printf("event queue: failed to persist pending event: %v", err)
		return
	}

	// Enforce cap: max 1000 pending events per target (evict oldest FIFO).
	h.enforcePendingCap(destID, 1000)

	// Attempt instant dispatch if target is running.
	var plugin models.Plugin
	if err := h.db().First(&plugin, "id = ?", destID).Error; err != nil {
		return
	}

	if plugin.Status == "running" && plugin.Host != "" {
		if h.tryDispatch(plugin, callbackPath, body) {
			// Success — remove from pending queue.
			h.db().Delete(&pending)
			h.Events.Emit(events.DebugEvent{
				Type:     "dispatch_ok",
				PluginID: destID,
				Detail:   fmt.Sprintf("event=%s from=%s callback=%s addressed=true", eventType, sourceID, callbackPath),
			})
			h.logEvent(eventType, sourceID, destID, "delivered", detail)
			return
		}
		// Dispatch failed — increment attempts, leave in queue.
		h.db().Model(&pending).Update("attempts", pending.Attempts+1)
		h.logEvent(eventType, sourceID, destID, "failed", "target unreachable")
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "dispatch_queued",
		PluginID: destID,
		Detail:   fmt.Sprintf("event=%s from=%s queued (target offline or unreachable)", eventType, sourceID),
	})
	h.logEvent(eventType, sourceID, destID, "queued", "target offline or unreachable")
}

// callbackPort returns the port to use for event callbacks.
// The SDK internal server (EventPort) only serves /events and /schema.
// Custom callback paths (e.g. /events/usage) are registered on the plugin's
// main HTTP server, so those must use HTTPPort.
func callbackPort(plugin models.Plugin, callbackPath string) int {
	if callbackPath == "/events" && plugin.EventPort > 0 {
		return plugin.EventPort
	}
	return plugin.HTTPPort
}

// tryDispatch attempts to deliver an event payload to a plugin via HTTP POST.
// Returns true on success (HTTP 200), false otherwise.
func (h *PluginHandler) tryDispatch(plugin models.Plugin, callbackPath string, body []byte) bool {
	targetURL := fmt.Sprintf("%s://%s:%d%s", h.pluginScheme(), plugin.Host, callbackPort(plugin, callbackPath), callbackPath)

	client := &http.Client{Timeout: 5 * time.Second, Transport: h.transport}

	resp, err := client.Post(targetURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// flushPendingEvents delivers all queued events for a target plugin.
// Called when a plugin registers (comes online).
func (h *PluginHandler) flushPendingEvents(targetPluginID string) {
	var pending []models.Event
	if err := h.db().Where("target_plugin_id = ?", targetPluginID).Order("created_at ASC").Find(&pending).Error; err != nil {
		log.Printf("event flush: failed to query pending events for %s: %v", targetPluginID, err)
		return
	}

	if len(pending) == 0 {
		return
	}

	log.Printf("event flush: delivering %d pending events to %s", len(pending), targetPluginID)

	var plugin models.Plugin
	if err := h.db().First(&plugin, "id = ?", targetPluginID).Error; err != nil {
		log.Printf("event flush: target plugin %s not found", targetPluginID)
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		log.Printf("event flush: target plugin %s not ready (status=%s)", targetPluginID, plugin.Status)
		return
	}

	for _, pe := range pending {
		// Re-lookup current subscription — the stored path may be stale if the
		// event was queued before the target plugin subscribed.
		callbackPath := pe.CallbackPath
		if sub, found := h.Subs.FindSubscription(targetPluginID, pe.EventType); found {
			callbackPath = sub.CallbackPath
		}

		if h.tryDispatch(plugin, callbackPath, []byte(pe.Payload)) {
			h.db().Delete(&pe)
			h.Events.Emit(events.DebugEvent{
				Type:     "dispatch_ok",
				PluginID: targetPluginID,
				Detail:   fmt.Sprintf("event=%s from=%s flushed", pe.EventType, pe.SourcePluginID),
			})
			h.logEvent(pe.EventType, pe.SourcePluginID, targetPluginID, "delivered", "flushed from queue")
		} else {
			h.db().Model(&pe).Update("attempts", pe.Attempts+1)
			h.Events.Emit(events.DebugEvent{
				Type:     "dispatch_error",
				PluginID: targetPluginID,
				Detail:   fmt.Sprintf("event=%s from=%s flush failed (attempt %d)", pe.EventType, pe.SourcePluginID, pe.Attempts+1),
			})
			h.logEvent(pe.EventType, pe.SourcePluginID, targetPluginID, "failed", fmt.Sprintf("flush attempt %d", pe.Attempts+1))
		}
	}
}

// enforcePendingCap ensures no more than maxCount pending events exist per target.
// Deletes oldest events (FIFO) if over the cap.
func (h *PluginHandler) enforcePendingCap(targetPluginID string, maxCount int) {
	var count int64
	h.db().Model(&models.Event{}).Where("target_plugin_id = ?", targetPluginID).Count(&count)
	if count <= int64(maxCount) {
		return
	}

	excess := int(count) - maxCount
	var oldest []models.Event
	h.db().Where("target_plugin_id = ?", targetPluginID).Order("created_at ASC").Limit(excess).Find(&oldest)
	for _, pe := range oldest {
		h.logEvent(pe.EventType, pe.SourcePluginID, pe.TargetPluginID, "evicted", fmt.Sprintf("cap=%d exceeded", maxCount))
		h.db().Delete(&pe)
	}
	log.Printf("event queue: evicted %d oldest events for %s (cap=%d)", excess, targetPluginID, maxCount)
}

// dispatchEvent delivers an event payload to a subscriber plugin via HTTP callback.
// Fire-and-forget: errors are logged but do not propagate.
func (h *PluginHandler) dispatchEvent(sub events.Subscription, body []byte) {
	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", sub.PluginID); result.Error != nil {
		log.Printf("event dispatch: subscriber %s not found in db", sub.PluginID)
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		h.Events.Emit(events.DebugEvent{
			Type:     "dispatch_error",
			PluginID: sub.PluginID,
			Detail:   fmt.Sprintf("event=%s skipped: status=%s host=%q (not ready)", sub.EventType, plugin.Status, plugin.Host),
		})
		log.Printf("event dispatch: skipping %s→%s: status=%s host=%q", sub.EventType, sub.PluginID, plugin.Status, plugin.Host)
		return
	}

	targetURL := fmt.Sprintf("%s://%s:%d%s", h.pluginScheme(), plugin.Host, callbackPort(plugin, sub.CallbackPath), sub.CallbackPath)

	client := &http.Client{Timeout: 5 * time.Second, Transport: h.transport}

	resp, err := client.Post(targetURL, "application/json", bytes.NewReader(body))
	if err != nil {
		h.Events.Emit(events.DebugEvent{
			Type:     "dispatch_error",
			PluginID: sub.PluginID,
			Detail:   fmt.Sprintf("event=%s error=%v", sub.EventType, err),
		})
		log.Printf("event dispatch: failed to deliver %s to %s (%s): %v", sub.EventType, sub.PluginID, targetURL, err)
		return
	}
	resp.Body.Close()

	h.Events.Emit(events.DebugEvent{
		Type:     "dispatch_ok",
		PluginID: sub.PluginID,
		Status:   resp.StatusCode,
		Detail:   fmt.Sprintf("event=%s callback=%s", sub.EventType, sub.CallbackPath),
	})
}

// UpdatePricing handles POST /api/plugins/pricing — allows plugins to push
// price updates to the kernel via service-token auth.
func (h *PluginHandler) UpdatePricing(c *gin.Context) {
	var req struct {
		Prices []struct {
			Provider    string  `json:"provider" binding:"required"`
			Model       string  `json:"model" binding:"required"`
			InputPer1M  float64 `json:"input_per_1m"`
			OutputPer1M float64 `json:"output_per_1m"`
			CachedPer1M float64 `json:"cached_per_1m"`
			PerRequest  float64 `json:"per_request"`
			Currency    string  `json:"currency"`
		} `json:"prices" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var saved []models.ModelPrice
	for _, p := range req.Prices {
		price, err := SavePriceRecord(h.db(), p.Provider, p.Model, p.InputPer1M, p.OutputPer1M, p.CachedPer1M, p.PerRequest, 0, p.Currency, time.Time{})
		if err != nil {
			log.Printf("pricing: failed to save %s/%s: %v", p.Provider, p.Model, err)
			continue
		}
		saved = append(saved, *price)
	}

	h.Events.Emit(events.DebugEvent{
		Type:   "pricing",
		Detail: fmt.Sprintf("updated %d model prices via plugin push", len(saved)),
	})

	c.JSON(http.StatusOK, gin.H{"message": "prices updated", "count": len(saved)})
}

// WebhookIngress handles public (unauthenticated) webhook traffic from external
// services like Telegram, Discord, etc. Emits "webhook" events instead of "proxy".
func (h *PluginHandler) WebhookIngress(c *gin.Context) {
	c.Set("event_type", "webhook")
	h.RouteToPlugin(c)
}

// RouteToPlugin handles POST /api/route/:plugin_id/*path — proxies requests
// through the kernel to the target plugin. Uses httputil.ReverseProxy to
// support both regular HTTP and WebSocket connections transparently.
func (h *PluginHandler) RouteToPlugin(c *gin.Context) {
	pluginID := c.Param("plugin_id")
	path := c.Param("path")
	start := time.Now()

	eventType := "proxy"
	if t, ok := c.Get("event_type"); ok {
		eventType = t.(string)
	}

	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ?", pluginID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("plugin %q not found", pluginID)})
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin not running"})
		return
	}

	target, _ := url.Parse(fmt.Sprintf("%s://%s:%d", h.pluginScheme(), plugin.Host, plugin.HTTPPort))
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Enable streaming support (SSE, chunked responses) by flushing every 100ms.
	proxy.FlushInterval = 100 * time.Millisecond

	// Use mTLS transport if configured.
	if h.clientTLS != nil {
		proxy.Transport = &http.Transport{TLSClientConfig: h.clientTLS}
	}

	// Rewrite the request path to strip the /api/route/:plugin_id prefix.
	c.Request.URL.Path = path
	c.Request.URL.Host = target.Host
	c.Request.Host = target.Host

	// Security: strip ALL X-User-* headers from the incoming request to prevent
	// identity spoofing. Then inject trusted values from JWT context if present.
	for key := range c.Request.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-user-") {
			c.Request.Header.Del(key)
		}
	}

	// Inject user identity headers from JWT context (authenticated routes only).
	// Webhook routes (event_type=webhook) must NOT have user headers.
	if eventType != "webhook" {
		if uid, exists := c.Get("user_id"); exists {
			c.Request.Header.Set("X-User-ID", fmt.Sprintf("%d", uid.(uint)))
		}
		if email, exists := c.Get("email"); exists {
			c.Request.Header.Set("X-User-Email", email.(string))
		}
		if role, exists := c.Get("role"); exists {
			c.Request.Header.Set("X-User-Role", role.(string))
		}
	}

	// Extract the caller's origin (scheme+host only) to restrict iframe embedding.
	var callerOrigin string
	if origin := c.Request.Header.Get("Origin"); origin != "" {
		callerOrigin = origin
	} else if referer := c.Request.Header.Get("Referer"); referer != "" {
		if u, err := url.Parse(referer); err == nil {
			callerOrigin = u.Scheme + "://" + u.Host
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		// Restrict iframe embedding — only the teamagentica UI can embed plugin pages.
		// 'self' is needed because code-server creates internal sub-iframes.
		if callerOrigin != "" {
			resp.Header.Set("Content-Security-Policy", fmt.Sprintf("frame-ancestors 'self' %s", callerOrigin))
		} else {
			resp.Header.Set("Content-Security-Policy", "frame-ancestors 'self'")
		}

		var detail string
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			detail = string(body)
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.Events.Emit(events.DebugEvent{
			Type:     eventType,
			PluginID: pluginID,
			Method:   c.Request.Method,
			Path:     path,
			Status:   resp.StatusCode,
			Duration: time.Since(start).Milliseconds(),
			Detail:   detail,
		})
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		h.Events.Emit(events.DebugEvent{
			Type:     eventType,
			PluginID: pluginID,
			Method:   c.Request.Method,
			Path:     path,
			Status:   502,
			Duration: time.Since(start).Milliseconds(),
			Detail:   err.Error(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"Plugin is not reachable — it may have stopped or is still starting up"}`)
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// --- inter-plugin event subscription handlers ---

// SubscribeEvent handles POST /api/plugins/subscribe — allows a plugin to subscribe
// to events of a given type. When such events are reported, the kernel will POST
// the event payload to the subscriber's callbackPath.
func (h *PluginHandler) SubscribeEvent(c *gin.Context) {
	var req struct {
		ID           string `json:"id" binding:"required"`
		EventType    string `json:"event_type" binding:"required"`
		CallbackPath string `json:"callback_path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.Subs.Subscribe(req.ID, req.EventType, req.CallbackPath)

	h.Events.Emit(events.DebugEvent{
		Type:     "subscribe",
		PluginID: req.ID,
		Detail:   fmt.Sprintf("event_type=%s callback_path=%s", req.EventType, req.CallbackPath),
	})

	// Flush any pending addressed events for this plugin — events may have been
	// queued before this subscription existed (e.g. webhook:plugin:url arrives
	// before the target has subscribed). flushPendingEvents re-looks up the
	// callback path so stale paths get corrected.
	go h.flushPendingEvents(req.ID)

	c.JSON(http.StatusOK, gin.H{"message": "subscribed"})
}

// UnsubscribeEvent handles POST /api/plugins/unsubscribe — removes a plugin's
// subscription to events of a given type.
func (h *PluginHandler) UnsubscribeEvent(c *gin.Context) {
	var req struct {
		ID        string `json:"id" binding:"required"`
		EventType string `json:"event_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.Subs.Unsubscribe(req.ID, req.EventType)

	h.Events.Emit(events.DebugEvent{
		Type:     "unsubscribe",
		PluginID: req.ID,
		Detail:   fmt.Sprintf("event_type=%s", req.EventType),
	})

	c.JSON(http.StatusOK, gin.H{"message": "unsubscribed"})
}

// logEvent persists an inter-plugin event record to the EventLog table
// and broadcasts it over the SSE stream so the dashboard updates in real-time.
func (h *PluginHandler) logEvent(eventType, sourceID, targetID, status, detail string) {
	entry := models.EventLog{
		EventType:      eventType,
		SourcePluginID: sourceID,
		TargetPluginID: targetID,
		Status:         status,
		Detail:         detail,
	}
	if err := h.db().Create(&entry).Error; err != nil {
		log.Printf("event log: failed to persist: %v", err)
		return
	}
	h.Events.EmitEvent(entry)
}
