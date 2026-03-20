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

// AuthRequired validates the Bearer token and injects claims into context.
// Accepts the token via Authorization header or session cookie (for iframe embedding).
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
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

// RequireCapability returns middleware that checks for a specific capability in the JWT.
func RequireCapability(cap string) gin.HandlerFunc {
	return func(c *gin.Context) {
		capsVal, exists := c.Get("capabilities")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no capabilities in token"})
			return
		}

		caps, ok := capsVal.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid capabilities"})
			return
		}

		for _, v := range caps {
			if v == cap {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
}

// PluginTokenAuth validates a plugin's identity.
// When mTLS is enabled, identity comes from the client certificate CN.
// When mTLS is disabled (dev mode), falls back to JWT bearer token validation.
func PluginTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try mTLS first — extract plugin ID from client certificate.
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

		// Fallback to JWT bearer token (non-mTLS / dev mode).
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization (no client cert or bearer token)"})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		claims, err := auth.ValidateToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("claims", claims)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Set("auth_method", "jwt")
		c.Next()
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
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Writer.Header().Set("X-XSS-Protection", "0")
		c.Next()
	}
}
