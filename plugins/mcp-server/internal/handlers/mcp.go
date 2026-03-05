package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// MCP handles POST /mcp — the Streamable HTTP MCP transport.
// Each request contains a single JSON-RPC message.
func (h *Handler) MCP(c *gin.Context) {
	if h.mcpSrv == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP server not initialized"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	if h.cfg.Debug {
		log.Printf("mcp-server: POST /mcp body=%s", string(body))
	}

	resp := h.mcpSrv.HandleMessage(body)

	// Notifications (e.g. notifications/initialized) return nil — no response needed.
	if resp == nil {
		c.Status(http.StatusNoContent)
		return
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("mcp-server: failed to marshal response: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if h.cfg.Debug {
		log.Printf("mcp-server: response=%s", string(respBytes))
	}

	c.Data(http.StatusOK, "application/json", respBytes)
}
