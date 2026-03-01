package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"roboslop/kernel/internal/database"
	"roboslop/kernel/internal/models"
)

// ListAuditLogs handles GET /api/audit.
// Query params: action, actor_id, limit, offset.
func ListAuditLogs(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 500 {
		limit = 500
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	query := database.DB.Model(&models.AuditLog{}).Order("timestamp DESC")

	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if actorID := c.Query("actor_id"); actorID != "" {
		query = query.Where("actor_id = ?", actorID)
	}

	var total int64
	query.Count(&total)

	var logs []models.AuditLog
	if result := query.Limit(limit).Offset(offset).Find(&logs); result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch audit logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"logs":   logs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
