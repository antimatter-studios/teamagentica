package handlers

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
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
	Token        string       `json:"token"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	User         storage.User `json:"user"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// issueRefreshToken creates and persists a refresh token for the given user,
// returning the raw token to send to the client.
func (h *Handler) issueRefreshToken(userID uint) (string, error) {
	raw, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", err
	}
	rt := storage.RefreshToken{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(auth.RefreshTokenExpiry()),
	}
	if err := h.db.CreateRefreshToken(&rt); err != nil {
		return "", err
	}
	return raw, nil
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

	events.PublishUserRegistered(h.sdk, int(user.ID), user.Email)

	refreshToken, _ := h.issueRefreshToken(user.ID)

	c.JSON(http.StatusCreated, authResponse{Token: token, RefreshToken: refreshToken, User: user})
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

	refreshToken, _ := h.issueRefreshToken(user.ID)

	c.JSON(http.StatusOK, authResponse{Token: token, RefreshToken: refreshToken, User: *user})
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
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("teamagentica_session", token, 86400, "/", cookieDomain, secure, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RefreshAccessToken handles POST /auth/refresh.
// Validates the refresh token and issues a new access token (+ new refresh token).
func (h *Handler) RefreshAccessToken(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashBytes := sha256.Sum256([]byte(req.RefreshToken))
	hash := fmt.Sprintf("%x", hashBytes)

	rt, err := h.db.GetRefreshTokenByHash(hash)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}

	// Revoke the used refresh token (single-use rotation).
	_ = h.db.RevokeRefreshToken(rt.ID)

	user, err := h.db.GetUserByID(rt.UserID)
	if err != nil || user.Banned {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "account unavailable"})
		return
	}

	token, err := auth.GenerateToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	newRefresh, _ := h.issueRefreshToken(user.ID)

	h.audit.LogUserAction(user.ID, "auth.refresh",
		fmt.Sprintf("user:%d", user.ID), "",
		c.ClientIP(), true)

	c.JSON(http.StatusOK, authResponse{Token: token, RefreshToken: newRefresh, User: *user})
}

// Logout handles POST /auth/logout — revokes all refresh tokens for the user.
func (h *Handler) Logout(c *gin.Context) {
	var userID uint
	if uid := c.GetHeader("X-User-ID"); uid != "" {
		fmt.Sscanf(uid, "%d", &userID)
	}
	if userID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing user"})
		return
	}
	_ = h.db.RevokeUserRefreshTokens(userID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

