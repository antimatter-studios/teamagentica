package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/auth"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

// Me handles GET /users/me.
// The kernel proxies this with claims info in headers.
func (h *Handler) Me(c *gin.Context) {
	// When proxied from kernel, user info arrives via headers.
	// When called directly (during bootstrap), validate token.
	userIDStr := c.GetHeader("X-User-ID")
	if userIDStr == "" {
		// Try extracting from Authorization header directly
		tokenStr := extractBearerToken(c)
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "no user context"})
			return
		}
		claims, err := auth.ValidateToken(tokenStr)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		user, err := h.db.GetUserByID(claims.UserID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user":         user,
			"capabilities": claims.Capabilities,
		})
		return
	}

	var userID uint
	fmt.Sscanf(userIDStr, "%d", &userID)

	user, err := h.db.GetUserByID(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Reconstruct capabilities from role
	caps := storage.GetCapabilities(user.Role)
	c.JSON(http.StatusOK, gin.H{
		"user":         user,
		"capabilities": caps,
	})
}

// ListUsers handles GET /users.
func (h *Handler) ListUsers(c *gin.Context) {
	users, err := h.db.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// GetUser handles GET /users/:id.
func (h *Handler) GetUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	user, err := h.db.GetUserByID(uint(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user": user})
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
}

// UpdateUser handles PUT /users/:id.
func (h *Handler) UpdateUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	user, err := h.db.GetUserByID(uint(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.DisplayName != nil {
		user.DisplayName = *req.DisplayName
	}
	if req.Role != nil {
		validRoles := map[string]bool{"admin": true, "user": true}
		if !validRoles[*req.Role] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role, must be 'admin' or 'user'"})
			return
		}
		user.Role = *req.Role
	}

	if err := h.db.UpdateUser(user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		return
	}

	h.audit.LogUserAction(user.ID, "user.updated",
		fmt.Sprintf("user:%d", user.ID),
		fmt.Sprintf(`{"display_name":%q,"role":%q}`, user.DisplayName, user.Role),
		c.ClientIP(), true)

	h.sdk.ReportEvent("user.updated", fmt.Sprintf(`{"user_id":%d}`, user.ID))

	c.JSON(http.StatusOK, gin.H{"user": user})
}

type banUserRequest struct {
	Banned bool   `json:"banned"`
	Reason string `json:"reason"`
}

// BanUser handles PUT /users/:id/ban.
func (h *Handler) BanUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	user, err := h.db.GetUserByID(uint(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}

	var req banUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user.Banned = req.Banned
	user.BanReason = req.Reason

	if err := h.db.UpdateUser(user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		return
	}

	action := "user.unbanned"
	if req.Banned {
		action = "user.banned"
	}

	h.audit.LogUserAction(user.ID, action,
		fmt.Sprintf("user:%d", user.ID),
		fmt.Sprintf(`{"banned":%t,"reason":%q}`, req.Banned, req.Reason),
		c.ClientIP(), true)

	h.sdk.ReportEvent(action, fmt.Sprintf(`{"user_id":%d,"email":%q}`, user.ID, user.Email))

	c.JSON(http.StatusOK, gin.H{"user": user})
}

// DeleteUser handles DELETE /users/:id.
func (h *Handler) DeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	if err := h.db.DeleteUser(uint(id)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}

	h.audit.LogUserAction(uint(id), "user.deleted",
		fmt.Sprintf("user:%d", id), "",
		c.ClientIP(), true)

	h.sdk.ReportEvent("user.deleted", fmt.Sprintf(`{"user_id":%d}`, id))

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// extractBearerToken gets the token from the Authorization header.
func extractBearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}
