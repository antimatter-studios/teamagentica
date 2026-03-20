package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/storage"
)

// Handler serves REST + MCP endpoints for memory management.
type Handler struct {
	db             *storage.DB
	maxMsgPerSession int
}

// New creates a Handler backed by the given DB.
func New(db *storage.DB, maxMsgPerSession int) *Handler {
	return &Handler{db: db, maxMsgPerSession: maxMsgPerSession}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ── Session REST API ───────────────────────────────────────────────────────────

// ListSessions handles GET /sessions — returns all session summaries.
func (h *Handler) ListSessions(c *gin.Context) {
	sessions, err := h.db.ListSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// GetHistory handles GET /sessions/:id/messages — returns recent messages for a session.
func (h *Handler) GetHistory(c *gin.Context) {
	sessionID := c.Param("id")
	limit := h.maxMsgPerSession
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	msgs, err := h.db.GetHistory(sessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "messages": msgs})
}

// AddMessage handles POST /sessions/:id/messages — appends a message to a session.
func (h *Handler) AddMessage(c *gin.Context) {
	sessionID := c.Param("id")
	var req struct {
		Role      string `json:"role" binding:"required"`
		Content   string `json:"content" binding:"required"`
		Responder string `json:"responder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role != "user" && req.Role != "assistant" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'user' or 'assistant'"})
		return
	}
	msg, err := h.db.AddMessage(sessionID, req.Role, req.Content, req.Responder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Prune after adding so the session stays within the configured limit.
	if h.maxMsgPerSession > 0 {
		_ = h.db.PruneSession(sessionID, h.maxMsgPerSession)
	}
	c.JSON(http.StatusCreated, msg)
}

// ClearSession handles DELETE /sessions/:id — wipes all messages for a session.
func (h *Handler) ClearSession(c *gin.Context) {
	sessionID := c.Param("id")
	if err := h.db.ClearSession(sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── MCP tool discovery ─────────────────────────────────────────────────────────

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tools": []gin.H{
			{
				"name":        "list_sessions",
				"description": "List all active conversation sessions with message counts and last activity time",
				"endpoint":    "/mcp/list_sessions",
				"parameters":  gin.H{"type": "object", "properties": gin.H{}},
			},
			{
				"name":        "get_history",
				"description": "Get conversation history for a session, ordered oldest-first",
				"endpoint":    "/mcp/get_history",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"session_id": gin.H{"type": "string", "description": "The session/channel ID"},
						"limit":      gin.H{"type": "integer", "description": "Max messages to return (default: 20)"},
					},
					"required": []string{"session_id"},
				},
			},
			{
				"name":        "add_message",
				"description": "Append a message to a conversation session",
				"endpoint":    "/mcp/add_message",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"session_id": gin.H{"type": "string", "description": "The session/channel ID"},
						"role":       gin.H{"type": "string", "enum": []string{"user", "assistant"}, "description": "Message author role"},
						"content":    gin.H{"type": "string", "description": "Message content"},
						"responder":  gin.H{"type": "string", "description": "Agent alias that produced this message (for assistant messages)"},
					},
					"required": []string{"session_id", "role", "content"},
				},
			},
			{
				"name":        "clear_session",
				"description": "Delete all messages in a conversation session",
				"endpoint":    "/mcp/clear_session",
				"parameters": gin.H{
					"type":       "object",
					"properties": gin.H{"session_id": gin.H{"type": "string", "description": "The session/channel ID to clear"}},
					"required":   []string{"session_id"},
				},
			},
		},
	})
}

// ── MCP tool execution ─────────────────────────────────────────────────────────

func (h *Handler) MCPListSessions(c *gin.Context) {
	sessions, err := h.db.ListSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *Handler) MCPGetHistory(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id" binding:"required"`
		Limit     int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	msgs, err := h.db.GetHistory(req.SessionID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session_id": req.SessionID, "messages": msgs})
}

func (h *Handler) MCPAddMessage(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id" binding:"required"`
		Role      string `json:"role" binding:"required"`
		Content   string `json:"content" binding:"required"`
		Responder string `json:"responder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	msg, err := h.db.AddMessage(req.SessionID, req.Role, req.Content, req.Responder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.maxMsgPerSession > 0 {
		_ = h.db.PruneSession(req.SessionID, h.maxMsgPerSession)
	}
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

func (h *Handler) MCPClearSession(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.ClearSession(req.SessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "session cleared"})
}
