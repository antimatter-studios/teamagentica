package handlers

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"roboslop/kernel/internal/models"
)

// proxyClient returns an http.Client for proxying requests to plugins.
// Uses mTLS if clientTLS is configured, otherwise uses the default client.
func (h *PluginHandler) proxyClient() *http.Client {
	if h.clientTLS != nil {
		return &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: h.clientTLS,
			},
		}
	}
	return http.DefaultClient
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
	ID           string   `json:"id" binding:"required"`
	Host         string   `json:"host" binding:"required"`
	Port         int      `json:"port" binding:"required"`
	Capabilities []string `json:"capabilities"`
	Version      string   `json:"version"`
}

type pluginHeartbeatRequest struct {
	ID string `json:"id" binding:"required"`
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

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", req.ID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found in registry"})
		return
	}

	now := time.Now()
	updates := map[string]interface{}{
		"host":      req.Host,
		"http_port": req.Port,
		"status":    "running",
		"last_seen": now,
	}

	if req.Version != "" {
		updates["version"] = req.Version
	}

	if req.Capabilities != nil {
		plugin.SetCapabilities(req.Capabilities)
		updates["capabilities"] = plugin.Capabilities
	}

	h.db.Model(&plugin).Updates(updates)

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
	if result := h.db.First(&plugin, "id = ?", req.ID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"last_seen": time.Now(),
		"status":    "running",
	})

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// Deregister handles POST /api/plugins/deregister — called by plugins on shutdown.
func (h *PluginHandler) Deregister(c *gin.Context) {
	var req pluginDeregisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", req.ID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	h.db.Model(&plugin).Updates(map[string]interface{}{
		"status": "stopped",
		"host":   "",
	})

	c.JSON(http.StatusOK, gin.H{"message": "deregistered"})
}

// RouteToPlugin handles POST /api/route/:plugin_id/*path — proxies requests
// through the kernel to the target plugin.
func (h *PluginHandler) RouteToPlugin(c *gin.Context) {
	pluginID := c.Param("plugin_id")
	path := c.Param("path")

	var plugin models.Plugin
	if result := h.db.First(&plugin, "id = ?", pluginID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found"})
		return
	}

	if plugin.Status != "running" || plugin.Host == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin not running"})
		return
	}

	targetURL := fmt.Sprintf("%s://%s:%d%s", h.pluginScheme(), plugin.Host, plugin.HTTPPort, path)

	proxyReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create proxy request"})
		return
	}

	// Forward all headers.
	for key, vals := range c.Request.Header {
		for _, val := range vals {
			proxyReq.Header.Add(key, val)
		}
	}

	// Forward query string.
	proxyReq.URL.RawQuery = c.Request.URL.RawQuery

	resp, err := h.proxyClient().Do(proxyReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "plugin unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for key, vals := range resp.Header {
		for _, val := range vals {
			c.Writer.Header().Add(key, val)
		}
	}

	c.Status(resp.StatusCode)
	io.Copy(c.Writer, resp.Body)
}
