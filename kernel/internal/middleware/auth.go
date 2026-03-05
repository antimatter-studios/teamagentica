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
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
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

// PluginTokenAuth validates a bearer token from a plugin service.
// Uses the same JWT validation as user auth but does not require user-specific claims.
func PluginTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		_, err := auth.ValidateToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Next()
	}
}

// CORS allows all origins for development.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
