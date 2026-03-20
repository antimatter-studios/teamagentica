package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

// ListExternalUsers handles GET /external-users.
func (h *Handler) ListExternalUsers(c *gin.Context) {
	source := c.Query("source")
	mappings, err := h.db.ListExternalUsers(source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query mappings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mappings": mappings})
}

type createExternalUserReq struct {
	ExternalID         string `json:"external_id" binding:"required"`
	Source             string `json:"source" binding:"required"`
	TeamagenticaUserID uint   `json:"teamagentica_user_id" binding:"required"`
	Label              string `json:"label"`
}

// CreateExternalUser handles POST /external-users.
func (h *Handler) CreateExternalUser(c *gin.Context) {
	var req createExternalUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	mapping := storage.ExternalUser{
		ExternalID:         req.ExternalID,
		Source:             req.Source,
		TeamagenticaUserID: req.TeamagenticaUserID,
		Label:              req.Label,
	}
	if err := h.db.CreateExternalUser(&mapping); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "mapping already exists or invalid: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, mapping)
}

type updateExternalUserReq struct {
	TeamagenticaUserID *uint   `json:"teamagentica_user_id"`
	Label              *string `json:"label"`
}

// UpdateExternalUser handles PUT /external-users/:id.
func (h *Handler) UpdateExternalUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	mapping, err := h.db.GetExternalUser(uint(id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch mapping"})
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

	if err := h.db.UpdateExternalUser(mapping); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update"})
		return
	}
	c.JSON(http.StatusOK, mapping)
}

// DeleteExternalUser handles DELETE /external-users/:id.
func (h *Handler) DeleteExternalUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.db.DeleteExternalUser(uint(id)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// LookupExternalUser handles GET /external-users/lookup.
// Query params: source, external_id
func (h *Handler) LookupExternalUser(c *gin.Context) {
	source := c.Query("source")
	externalID := c.Query("external_id")
	if source == "" || externalID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and external_id are required"})
		return
	}

	mappings, err := h.db.ListExternalUsers(source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query mappings"})
		return
	}

	for _, m := range mappings {
		if m.ExternalID == externalID {
			// Resolve the teamagentica user
			user, err := h.db.GetUserByID(m.TeamagenticaUserID)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"mapping": m, "user": nil})
				return
			}
			c.JSON(http.StatusOK, gin.H{"mapping": m, "user": user})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no mapping for %s:%s", source, externalID)})
}
