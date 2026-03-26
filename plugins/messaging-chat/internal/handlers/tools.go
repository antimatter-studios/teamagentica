package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/storage"
)

// Tools handles GET /mcp — returns tool definitions for MCP discovery.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// ToolDefs returns the MCP tool definitions for chat operations.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "list_conversations",
			"description": "List chat conversations. Returns conversation ID, title, message count, and timestamps.",
			"endpoint":    "/mcp/list_conversations",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id": gin.H{
						"type":        "integer",
						"description": "Filter by user ID. Omit to list all conversations.",
					},
					"limit": gin.H{
						"type":        "integer",
						"description": "Maximum number of conversations to return (default 50).",
					},
				},
			},
		},
		{
			"name":        "get_messages",
			"description": "Read messages from a chat conversation. Returns message content, role, agent info, and timestamps.",
			"endpoint":    "/mcp/get_messages",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"conversation_id": gin.H{
						"type":        "integer",
						"description": "The conversation ID to read messages from.",
					},
					"limit": gin.H{
						"type":        "integer",
						"description": "Maximum number of recent messages to return (default all).",
					},
				},
				"required": []string{"conversation_id"},
			},
		},
		{
			"name":        "post_message",
			"description": "Post a message into a chat conversation. The message appears in the web chat UI as an assistant message.",
			"endpoint":    "/mcp/post_message",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"conversation_id": gin.H{
						"type":        "integer",
						"description": "The conversation ID to post the message into.",
					},
					"content": gin.H{
						"type":        "string",
						"description": "The message content to post.",
					},
					"agent_alias": gin.H{
						"type":        "string",
						"description": "The agent alias to attribute the message to (e.g. 'claude', 'gemini').",
					},
				},
				"required": []string{"conversation_id", "content"},
			},
		},
		{
			"name":        "create_conversation",
			"description": "Create a new chat conversation. Returns the conversation ID for use with other chat tools.",
			"endpoint":    "/mcp/create_conversation",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id": gin.H{
						"type":        "integer",
						"description": "The user ID to create the conversation for.",
					},
					"title": gin.H{
						"type":        "string",
						"description": "Conversation title (default 'New Chat').",
					},
				},
				"required": []string{"user_id"},
			},
		},
	}
}

// --- Tool Handlers ---

type toolListConversationsReq struct {
	UserID uint `json:"user_id"`
	Limit  int  `json:"limit"`
}

func (h *Handler) ToolListConversations(c *gin.Context) {
	var req toolListConversationsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	var convos []storage.Conversation
	q := h.db.DB().Order("updated_at DESC").Limit(limit)
	if req.UserID > 0 {
		q = q.Where("user_id = ?", req.UserID)
	}
	if err := q.Find(&convos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Count messages per conversation.
	type result struct {
		ID           uint      `json:"id"`
		UserID       uint      `json:"user_id"`
		Title        string    `json:"title"`
		MessageCount int64     `json:"message_count"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	results := make([]result, len(convos))
	for i, conv := range convos {
		var count int64
		h.db.DB().Model(&storage.Message{}).
			Where("conversation_id = ? AND role IN ('user','assistant')", conv.ID).
			Count(&count)
		results[i] = result{
			ID:           conv.ID,
			UserID:       conv.UserID,
			Title:        conv.Title,
			MessageCount: count,
			CreatedAt:    conv.CreatedAt,
			UpdatedAt:    conv.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{"conversations": results})
}

type toolGetMessagesReq struct {
	ConversationID uint `json:"conversation_id"`
	Limit          int  `json:"limit"`
}

func (h *Handler) ToolGetMessages(c *gin.Context) {
	var req toolGetMessagesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.ConversationID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation_id required"})
		return
	}

	conv, err := h.db.GetConversation(req.ConversationID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	var msgs []storage.Message
	if req.Limit > 0 {
		msgs, err = h.db.ListMessagesForContext(req.ConversationID, req.Limit)
	} else {
		msgs, err = h.db.ListMessages(req.ConversationID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Filter out progress messages — tools should only see real messages.
	filtered := make([]storage.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "progress" {
			filtered = append(filtered, m)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"conversation": gin.H{
			"id":    conv.ID,
			"title": conv.Title,
		},
		"messages": filtered,
	})
}

type toolPostMessageReq struct {
	ConversationID uint   `json:"conversation_id"`
	Content        string `json:"content"`
	AgentAlias     string `json:"agent_alias"`
}

func (h *Handler) ToolPostMessage(c *gin.Context) {
	var req toolPostMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.ConversationID == 0 || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation_id and content required"})
		return
	}

	if _, err := h.db.GetConversation(req.ConversationID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	msg := &storage.Message{
		ConversationID: req.ConversationID,
		Role:           "assistant",
		Content:        req.Content,
		AgentAlias:     req.AgentAlias,
	}
	if err := h.db.CreateMessage(msg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update conversation timestamp so it surfaces in recents.
	if conv, err := h.db.GetConversation(req.ConversationID); err == nil {
		conv.UpdatedAt = time.Now()
		h.db.UpdateConversation(conv)
	}

	c.JSON(http.StatusCreated, gin.H{"message": msg})
}

type toolCreateConversationReq struct {
	UserID uint   `json:"user_id"`
	Title  string `json:"title"`
}

func (h *Handler) ToolCreateConversation(c *gin.Context) {
	var req toolCreateConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.UserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}

	title := req.Title
	if title == "" {
		title = "New Chat"
	}

	conv := &storage.Conversation{
		UserID: req.UserID,
		Title:  title,
	}
	if err := h.db.CreateConversation(conv); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"conversation": conv})
}
