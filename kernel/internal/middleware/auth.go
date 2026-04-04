package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/audit"
	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
)

// AuditInjector adds the audit logger to the Gin context so handlers can use it.
func AuditInjector(logger *audit.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("audit", logger)
		c.Next()
	}
}

// AuthOptional extracts and validates the Bearer token if present, injecting claims into context.
// Does not block the request if no token is provided — allows the handler to decide.
func AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.Next()
			return
		}

		claims, err := auth.ValidateToken(parts[1])
		if err != nil {
			c.Next()
			return
		}

		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("capabilities", claims.Capabilities)
		c.Next()
	}
}

// AuthRequired validates the caller's identity and injects claims into context.
// Accepts: mTLS client certificate (plugin-to-plugin calls), JWT Bearer token, or session cookie.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Accept mTLS client certificate (plugin-to-plugin routing).
		if c.Request.TLS != nil && len(c.Request.TLS.PeerCertificates) > 0 {
			cert := c.Request.TLS.PeerCertificates[0]
			pluginID := cert.Subject.CommonName
			if pluginID != "" {
				c.Set("plugin_id", pluginID)
				c.Set("auth_method", "mtls")
				c.Set("email", "plugin:"+pluginID)
				c.Set("role", "service")
				c.Next()
				return
			}
		}

		var token string

		header := c.GetHeader("Authorization")
		if header != "" {
			parts := strings.SplitN(header, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token = parts[1]
			}
		}

		// Fall back to cookie (used by iframe embeds like code-server).
		if token == "" {
			if cookie, err := c.Cookie("teamagentica_session"); err == nil {
				token = cookie
			}
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization"})
			return
		}

		claims, err := auth.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("capabilities", claims.Capabilities)
		c.Next()
	}
}


// PluginTokenAuth validates a plugin's identity via mTLS client certificate.
// The plugin ID is extracted from the certificate's Common Name (CN).
func PluginTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.TLS != nil && len(c.Request.TLS.PeerCertificates) > 0 {
			cert := c.Request.TLS.PeerCertificates[0]
			pluginID := cert.Subject.CommonName
			if pluginID != "" {
				c.Set("plugin_id", pluginID)
				c.Set("auth_method", "mtls")
				c.Set("email", "plugin:"+pluginID)
				c.Set("role", "service")
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "valid client certificate required for plugin auth"})
	}
}

// CORS validates the request origin against the configured base domain
// and only sets CORS headers for matching origins (exact or subdomain).
func CORS(baseDomain string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, baseDomain) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Writer.Header().Set("Access-Control-Max-Age", "86400")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// isAllowedOrigin checks whether the origin's host matches the base domain
// exactly or is a subdomain of it (e.g. "app.teamagentica.localhost").
func isAllowedOrigin(origin, baseDomain string) bool {
	// Strip scheme to get the host.
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip port if present.
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	// Strip trailing slash.
	host = strings.TrimRight(host, "/")

	if host == baseDomain {
		return true
	}
	return strings.HasSuffix(host, "."+baseDomain)
}

// SecurityHeaders adds standard security headers to every response.
// Workspace proxy paths (/ws/) are excluded from X-Frame-Options
// because the dashboard embeds them in iframes.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.HasPrefix(c.Request.URL.Path, "/ws/") {
			c.Writer.Header().Set("X-Frame-Options", "DENY")
		}
		c.Writer.Header().Set("X-XSS-Protection", "0")
		c.Next()
	}
}
