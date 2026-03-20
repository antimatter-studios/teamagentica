package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/audit"
)

// getAudit extracts the audit logger from the gin context.
func getAudit(c *gin.Context) *audit.Logger {
	if v, ok := c.Get("audit"); ok {
		return v.(*audit.Logger)
	}
	return nil
}
