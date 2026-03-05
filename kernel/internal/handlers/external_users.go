package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// ExternalUserHandler manages external user mapping CRUD.
type ExternalUserHandler struct {
	db *gorm.DB
}

// NewExternalUserHandler creates a new ExternalUserHandler.
func NewExternalUserHandler(db *gorm.DB) *ExternalUserHandler {
	return &ExternalUserHandler{db: db}
}

// List returns all external user mappings, optionally filtered by ?source=.
// GET /api/external-users
func (h *ExternalUserHandler) List(c *gin.Context) {
	var mappings []models.ExternalUser
	q := h.db.Order("source, external_id")
	if source := c.Query("source"); source != "" {
		q = q.Where("source = ?", source)
	}
	if err := q.Find(&mappings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query mappings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mappings": mappings})
}

type createExternalUserReq struct {
	ExternalID     string `json:"external_id" binding:"required"`
	Source         string `json:"source" binding:"required"`
	TeamagenticaUserID uint   `json:"teamagentica_user_id" binding:"required"`
	Label          string `json:"label"`
}

// Create adds a new external user mapping.
// POST /api/external-users
func (h *ExternalUserHandler) Create(c *gin.Context) {
	var req createExternalUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	mapping := models.ExternalUser{
		ExternalID:     req.ExternalID,
		Source:         req.Source,
		TeamagenticaUserID: req.TeamagenticaUserID,
		Label:          req.Label,
	}
	if err := h.db.Create(&mapping).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "mapping already exists or invalid: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, mapping)
}

type updateExternalUserReq struct {
	TeamagenticaUserID *uint   `json:"teamagentica_user_id"`
	Label          *string `json:"label"`
}

// Update modifies an existing external user mapping.
// PUT /api/external-users/:id
func (h *ExternalUserHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var mapping models.ExternalUser
	if err := h.db.First(&mapping, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	var req updateExternalUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.TeamagenticaUserID != nil {
		mapping.TeamagenticaUserID = *req.TeamagenticaUserID
	}
	if req.Label != nil {
		mapping.Label = *req.Label
	}

	if err := h.db.Save(&mapping).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update"})
		return
	}
	c.JSON(http.StatusOK, mapping)
}

// Delete removes an external user mapping.
// DELETE /api/external-users/:id
func (h *ExternalUserHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	result := h.db.Delete(&models.ExternalUser{}, uint(id))
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
