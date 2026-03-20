package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/audit"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

// Handler provides HTTP handlers for the user manager plugin.
type Handler struct {
	db    *storage.DB
	sdk   *pluginsdk.Client
	audit *audit.Logger
}

// New creates a new Handler.
func New(db *storage.DB, sdk *pluginsdk.Client, auditLogger *audit.Logger) *Handler {
	return &Handler{db: db, sdk: sdk, audit: auditLogger}
}

// Health handles GET /health.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
