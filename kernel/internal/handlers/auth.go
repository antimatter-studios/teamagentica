package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/audit"
	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// getAudit extracts the audit logger from the gin context.
func getAudit(c *gin.Context) *audit.Logger {
	if v, ok := c.Get("audit"); ok {
		return v.(*audit.Logger)
	}
	return nil
}

type registerRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=8"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type authResponse struct {
	Token string       `json:"token"`
	User  models.User  `json:"user"`
}

// Register handles POST /api/auth/register.
func Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Count existing users to determine mode.
	var count int64
	database.DB.Model(&models.User{}).Count(&count)

	// If users exist, only authenticated admins can register new users.
	if count > 0 {
		role, exists := c.Get("role")
		if !exists || role.(string) != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "registration closed. contact an admin to create your account"})
			return
		}
	}

	// Check if email already exists.
	var existing models.User
	if result := database.DB.Where("email = ?", req.Email).First(&existing); result.Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	// First user becomes admin.
	role := "user"
	if count == 0 {
		role = "admin"
	}

	user := models.User{
		Email:        req.Email,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Role:         role,
	}

	if result := database.DB.Create(&user); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	token, err := auth.GenerateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	if al := getAudit(c); al != nil {
		al.LogUserAction(user.ID, "auth.register",
			fmt.Sprintf("user:%d", user.ID),
			fmt.Sprintf(`{"email":%q,"role":%q}`, user.Email, user.Role),
			c.ClientIP(), true)
	}

	c.JSON(http.StatusCreated, authResponse{Token: token, User: user})
}

// Login handles POST /api/auth/login.
func Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if result := database.DB.Where("email = ?", req.Email).First(&user); result.Error != nil {
		if al := getAudit(c); al != nil {
			al.Log(models.AuditLog{
				ActorType: "user",
				ActorID:   req.Email,
				Action:    "auth.login",
				Detail:    `{"reason":"unknown email"}`,
				IP:        c.ClientIP(),
				Success:   false,
			})
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		if al := getAudit(c); al != nil {
			al.LogUserAction(user.ID, "auth.login",
				fmt.Sprintf("user:%d", user.ID),
				`{"reason":"bad password"}`,
				c.ClientIP(), false)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := auth.GenerateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	if al := getAudit(c); al != nil {
		al.LogUserAction(user.ID, "auth.login",
			fmt.Sprintf("user:%d", user.ID), "",
			c.ClientIP(), true)
	}

	c.JSON(http.StatusOK, authResponse{Token: token, User: user})
}

// CreateSession sets an HttpOnly session cookie containing the caller's JWT.
// Used by iframe embeds (e.g. code-server) that cannot send Authorization headers.
// The caller must already be authenticated via Bearer token.
func CreateSession(c *gin.Context) {
	// Re-extract the Bearer token from the header (already validated by AuthRequired).
	header := c.GetHeader("Authorization")
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing bearer token"})
		return
	}
	token := parts[1]

	// Set HttpOnly cookie on the parent domain so it's shared across subdomains
	// (e.g. api.teamagentica.localhost and code.teamagentica.localhost).
	cookieDomain := ""
	if host := c.Request.Host; host != "" {
		// Strip port if present.
		if idx := strings.LastIndex(host, ":"); idx > 0 {
			host = host[:idx]
		}
		// Strip first subdomain to get parent (api.example.com → example.com).
		if dot := strings.Index(host, "."); dot >= 0 {
			cookieDomain = host[dot:] // ".example.com"
		}
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("teamagentica_session", token, 86400, "/", cookieDomain, false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- service token types ---

type createServiceTokenRequest struct {
	Name         string   `json:"name" binding:"required"`
	Capabilities []string `json:"capabilities" binding:"required"`
	ExpiresInDays int     `json:"expires_in_days"`
}

type serviceTokenResponse struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
}

// AllowedServiceCapabilities is the whitelist of capabilities that can be granted to service tokens.
var AllowedServiceCapabilities = map[string]bool{
	"plugins:search":  true,
	"plugins:manage":  true,
	"users:read":      true,
	"system:admin":    true,
}

// --- service token handlers ---

// CreateServiceToken handles POST /api/auth/service-token.
func CreateServiceToken(c *gin.Context) {
	var req createServiceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default expiry to 365 days.
	expiresInDays := req.ExpiresInDays
	if expiresInDays <= 0 {
		expiresInDays = 365
	}

	// Validate capabilities against whitelist.
	for _, cap := range req.Capabilities {
		if !AllowedServiceCapabilities[cap] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capability: " + cap})
			return
		}
	}

	// Check for duplicate name.
	var existing models.ServiceToken
	if result := database.DB.Where("name = ?", req.Name).First(&existing); result.Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "service token with this name already exists"})
		return
	}

	// Generate the JWT.
	expiry := time.Duration(expiresInDays) * 24 * time.Hour
	token, err := auth.GenerateServiceToken(req.Name, req.Capabilities, expiry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	// Hash the token for storage.
	hash := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", hash)

	// Serialize capabilities.
	capsJSON, _ := json.Marshal(req.Capabilities)

	// Get issuing user ID from claims.
	var issuedBy uint
	if uid, exists := c.Get("user_id"); exists {
		issuedBy = uid.(uint)
	}

	expiresAt := time.Now().Add(expiry)
	st := models.ServiceToken{
		Name:         req.Name,
		TokenHash:    tokenHash,
		Capabilities: string(capsJSON),
		IssuedBy:     issuedBy,
		ExpiresAt:    expiresAt,
	}

	if result := database.DB.Create(&st); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save service token"})
		return
	}

	c.JSON(http.StatusCreated, serviceTokenResponse{
		Token:     token,
		Name:      req.Name,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

// ListServiceTokens handles GET /api/auth/service-tokens.
func ListServiceTokens(c *gin.Context) {
	var tokens []models.ServiceToken
	if result := database.DB.Find(&tokens); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch service tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}

// RevokeServiceToken handles DELETE /api/auth/service-token/:id.
func RevokeServiceToken(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}

	var token models.ServiceToken
	if result := database.DB.First(&token, id); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service token not found"})
		return
	}

	if result := database.DB.Model(&token).Update("revoked", true); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "service token revoked"})
}
