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
			"candidate_host":      req.Host,
			"candidate_port":      req.Port,
			"candidate_healthy":   true,
			"candidate_last_seen": now,
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
		"host":      req.Host,
		"http_port": req.Port,
		"status":    "running",
		"last_seen": now,
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

	// Broadcast plugin:ready lifecycle event directly to all running plugins
	// so they can update their peer address caches for P2P communication.
	go h.broadcastLifecycleEvent("plugin:ready", map[string]interface{}{
		"type":      "plugin:ready",
		"plugin_id": req.ID,
		"host":      req.Host,
		"http_port": req.Port,
	})

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

	// Heartbeats are not emitted to the event hub — they flood the stream
	// and push out useful events. The health monitor tracks heartbeats via
	// the last_seen timestamp in the database.

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

	h.Events.Emit(events.DebugEvent{
		Type:     "deregister",
		PluginID: req.ID,
	})

	// Broadcast plugin:stopped so peers invalidate their address caches.
	go h.broadcastLifecycleEvent("plugin:stopped", map[string]interface{}{
		"type":      "plugin:stopped",
		"plugin_id": req.ID,
	})

	c.JSON(http.StatusOK, gin.H{"message": "deregistered"})
}

// ReportEvent handles POST /api/plugins/event — emits to the debug SSE console.
// Inter-plugin event routing is now handled by infra-redis via Redis Streams.
// This endpoint is kept for backward compatibility and debug console observability.
func (h *PluginHandler) ReportEvent(c *gin.Context) {
	var req struct {
		ID          string `json:"id" binding:"required"`
		Type        string `json:"type" binding:"required"`
		Detail      string `json:"detail"`
		Destination string `json:"destination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Emit to SSE debug console for TUI/dashboard observability.
	h.Events.Emit(events.DebugEvent{
		Type:     req.Type,
		PluginID: req.ID,
		Detail:   req.Detail,
	})

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// UpdatePricing handles POST /api/plugins/pricing — forwards price updates
// to infra-cost-tracking plugin which now owns all pricing data.
func (h *PluginHandler) UpdatePricing(c *gin.Context) {
	// Read the raw body to forward as-is.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Look up infra-cost-tracking plugin.
	var plugin models.Plugin
	if result := h.db().First(&plugin, "id = ? AND status = 'running'", "infra-cost-tracking"); result.Error != nil {
		log.Printf("pricing: infra-cost-tracking not running, cannot forward prices: %v", result.Error)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "infra-cost-tracking plugin not available"})
		return
	}

	targetURL := fmt.Sprintf("%s://%s:%d/pricing/push", h.pluginScheme(), plugin.Host, plugin.HTTPPort)
	resp, err := h.proxyClient().Post(targetURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("pricing: failed to forward to infra-cost-tracking: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach infra-cost-tracking"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	h.Events.Emit(events.DebugEvent{
		Type:   "pricing",
		Detail: "forwarded pricing push to infra-cost-tracking",
	})

	c.Data(resp.StatusCode, "application/json", respBody)
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
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": fmt.Sprintf("plugin %q not running", pluginID)})
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
		// Only emit on errors — successful proxies are noise.
		if resp.StatusCode >= 400 {
			h.Events.Emit(events.DebugEvent{
				Type:     eventType + "-error",
				PluginID: pluginID,
				Method:   c.Request.Method,
				Path:     path,
				Status:   resp.StatusCode,
				Duration: time.Since(start).Milliseconds(),
				Detail:   detail,
			})
		}
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
		fmt.Fprintf(w, `{"error":"Plugin '%s' is not reachable — it may have stopped or is still starting up"}`, pluginID)
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// --- inter-plugin event subscription handlers ---
// Event routing is now handled by infra-redis via Redis Streams.
// These endpoints are kept as stubs for SDK backward compatibility during transition.

// SubscribeEvent handles POST /api/plugins/subscribe — stub for backward compat.
// Actual subscription now happens via infra-redis consumer groups.
func (h *PluginHandler) SubscribeEvent(c *gin.Context) {
	var req struct {
		ID           string `json:"id" binding:"required"`
		EventType    string `json:"event_type" binding:"required"`
		CallbackPath string `json:"callback_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.Events.Emit(events.DebugEvent{
		Type:     "subscribe",
		PluginID: req.ID,
		Detail:   fmt.Sprintf("event_type=%s (routed via redis)", req.EventType),
	})

	c.JSON(http.StatusOK, gin.H{"message": "subscribed"})
}

// UnsubscribeEvent handles POST /api/plugins/unsubscribe — stub for backward compat.
func (h *PluginHandler) UnsubscribeEvent(c *gin.Context) {
	var req struct {
		ID        string `json:"id" binding:"required"`
		EventType string `json:"event_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "unsubscribed"})
}


