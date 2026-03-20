package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/auth"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

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
	User  storage.User `json:"user"`
}

// Register handles POST /auth/register.
func (h *Handler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	count, err := h.db.UserCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check user count"})
		return
	}

	// If users exist, only authenticated admins can register new users.
	if count > 0 {
		role := c.GetHeader("X-User-Role")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "registration closed. contact an admin to create your account"})
			return
		}
	}

	// Check if email already exists.
	if existing, _ := h.db.GetUserByEmail(req.Email); existing != nil {
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

	user := storage.User{
		Email:        req.Email,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Role:         role,
	}

	if err := h.db.CreateUser(&user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	token, err := auth.GenerateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	h.audit.LogUserAction(user.ID, "auth.register",
		fmt.Sprintf("user:%d", user.ID),
		fmt.Sprintf(`{"email":%q,"role":%q}`, user.Email, user.Role),
		c.ClientIP(), true)

	h.sdk.ReportEvent("user.registered", fmt.Sprintf(`{"user_id":%d,"email":%q}`, user.ID, user.Email))

	c.JSON(http.StatusCreated, authResponse{Token: token, User: user})
}

// Login handles POST /auth/login.
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.db.GetUserByEmail(req.Email)
	if err != nil {
		h.audit.Log(storage.AuditLog{
			ActorType: "user",
			ActorID:   req.Email,
			Action:    "auth.login",
			Detail:    `{"reason":"unknown email"}`,
			IP:        c.ClientIP(),
			Success:   false,
		})
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if user.Banned {
		h.audit.LogUserAction(user.ID, "auth.login",
			fmt.Sprintf("user:%d", user.ID),
			`{"reason":"account banned"}`,
			c.ClientIP(), false)
		c.JSON(http.StatusForbidden, gin.H{"error": "account has been banned"})
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		h.audit.LogUserAction(user.ID, "auth.login",
			fmt.Sprintf("user:%d", user.ID),
			`{"reason":"bad password"}`,
			c.ClientIP(), false)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := auth.GenerateToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	h.audit.LogUserAction(user.ID, "auth.login",
		fmt.Sprintf("user:%d", user.ID), "",
		c.ClientIP(), true)

	c.JSON(http.StatusOK, authResponse{Token: token, User: *user})
}

// CreateSession sets an HttpOnly session cookie containing the caller's JWT.
func (h *Handler) CreateSession(c *gin.Context) {
	header := c.GetHeader("Authorization")
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing bearer token"})
		return
	}
	token := parts[1]

	cookieDomain := ""
	if host := c.Request.Host; host != "" {
		if idx := strings.LastIndex(host, ":"); idx > 0 {
			host = host[:idx]
		}
		if dot := strings.Index(host, "."); dot >= 0 {
			cookieDomain = host[dot:]
		}
	}
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("teamagentica_session", token, 86400, "/", cookieDomain, secure, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- Service Token handlers ---

type createServiceTokenRequest struct {
	Name          string   `json:"name" binding:"required"`
	Capabilities  []string `json:"capabilities" binding:"required"`
	ExpiresInDays int      `json:"expires_in_days"`
}

type serviceTokenResponse struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
}

var AllowedServiceCapabilities = map[string]bool{
	"plugins:search": true,
	"plugins:manage": true,
	"users:read":     true,
	"system:admin":   true,
}

// CreateServiceToken handles POST /auth/service-token.
func (h *Handler) CreateServiceToken(c *gin.Context) {
	var req createServiceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	expiresInDays := req.ExpiresInDays
	if expiresInDays <= 0 {
		expiresInDays = 365
	}

	for _, cap := range req.Capabilities {
		if !AllowedServiceCapabilities[cap] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capability: " + cap})
			return
		}
	}

	expiry := time.Duration(expiresInDays) * 24 * time.Hour
	token, err := auth.GenerateServiceToken(req.Name, req.Capabilities, expiry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	hashBytes := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", hashBytes)

	capsJSON, _ := json.Marshal(req.Capabilities)

	userID := uint(0)
	if uid := c.GetHeader("X-User-ID"); uid != "" {
		fmt.Sscanf(uid, "%d", &userID)
	}

	expiresAt := time.Now().Add(expiry)
	st := storage.ServiceToken{
		Name:         req.Name,
		TokenHash:    tokenHash,
		Capabilities: string(capsJSON),
		IssuedBy:     userID,
		ExpiresAt:    expiresAt,
	}

	if err := h.db.CreateServiceToken(&st); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "service token with this name already exists"})
		return
	}

	c.JSON(http.StatusCreated, serviceTokenResponse{
		Token:     token,
		Name:      req.Name,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

// ListServiceTokens handles GET /auth/service-tokens.
func (h *Handler) ListServiceTokens(c *gin.Context) {
	tokens, err := h.db.ListServiceTokens()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch service tokens"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}

// RevokeServiceToken handles DELETE /auth/service-token/:id.
func (h *Handler) RevokeServiceToken(c *gin.Context) {
	var id uint
	if _, err := fmt.Sscanf(c.Param("id"), "%d", &id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}

	if err := h.db.RevokeServiceToken(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service token not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "service token revoked"})
}
