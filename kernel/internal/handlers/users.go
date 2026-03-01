package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"roboslop/kernel/internal/auth"
	"roboslop/kernel/internal/database"
	"roboslop/kernel/internal/models"
)

// Me handles GET /api/users/me.
func Me(c *gin.Context) {
	claimsVal, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no claims in context"})
		return
	}

	claims := claimsVal.(*auth.Claims)

	var user models.User
	if result := database.DB.First(&user, claims.UserID); result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user":         user,
		"capabilities": claims.Capabilities,
	})
}

// ListUsers handles GET /api/users.
func ListUsers(c *gin.Context) {
	var users []models.User
	if result := database.DB.Find(&users); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch users"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}
