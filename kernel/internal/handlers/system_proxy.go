package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// SystemPluginProxy returns a gin.HandlerFunc that proxies requests to a fixed
// system plugin. The request path is rewritten by stripping the /api prefix.
// For example, POST /api/auth/login → POST /auth/login on the plugin.
func (h *PluginHandler) SystemPluginProxy(pluginID, pathPrefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		var plugin models.Plugin
		if result := h.db().First(&plugin, "id = ?", pluginID); result.Error != nil {
			database.CheckError(result.Error)

			// If CheckError reconnected the DB, retry with the fresh connection.
			if result2 := h.db().First(&plugin, "id = ?", pluginID); result2.Error != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "system plugin not found: " + pluginID})
				return
			}
		}

		if plugin.Host == "" || plugin.HTTPPort == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "system plugin not running: " + pluginID})
			return
		}

		target, _ := url.Parse(fmt.Sprintf("%s://%s:%d", h.pluginScheme(), plugin.Host, plugin.HTTPPort))
		proxy := httputil.NewSingleHostReverseProxy(target)

		// Enable streaming support (SSE, chunked responses).
		proxy.FlushInterval = 100 * time.Millisecond
		proxy.Transport = h.transport

		// Strip /api prefix so plugin gets e.g. /auth/login instead of /api/auth/login.
		path := c.Request.URL.Path
		if len(path) > 4 && path[:4] == "/api" {
			path = path[4:]
		}

		c.Request.URL.Path = path
		c.Request.URL.Host = target.Host
		c.Request.Host = target.Host

		// Inject user context headers for the plugin.
		if uid, exists := c.Get("user_id"); exists {
			c.Request.Header.Set("X-User-ID", fmt.Sprintf("%d", uid.(uint)))
		}
		if email, exists := c.Get("email"); exists {
			c.Request.Header.Set("X-User-Email", email.(string))
		}
		if role, exists := c.Get("role"); exists {
			c.Request.Header.Set("X-User-Role", role.(string))
		}

		proxy.ModifyResponse = func(resp *http.Response) error {
			var detail string
			if resp.StatusCode >= 400 {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				detail = string(body)
				resp.Body = io.NopCloser(bytes.NewReader(body))
			}
			h.Events.Emit(events.DebugEvent{
				Type:     "system-proxy",
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
				Type:     "system-proxy",
				PluginID: pluginID,
				Method:   c.Request.Method,
				Path:     path,
				Status:   502,
				Duration: time.Since(start).Milliseconds(),
				Detail:   err.Error(),
			})
			c.JSON(http.StatusBadGateway, gin.H{"error": "system plugin unreachable: " + pluginID})
		}

		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
