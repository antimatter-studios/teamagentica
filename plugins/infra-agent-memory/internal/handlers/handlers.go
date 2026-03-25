package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/storage"
)

// Handler serves REST + MCP endpoints for memory management.
type Handler struct {
	db               *storage.DB
	maxMsgPerSession int
	sdk              *pluginsdk.Client
	compactor        *Compactor
	activity         *ActivityLog

	mu                sync.RWMutex
	extractionPersona string // alias without '@', e.g. "brains"
}

// New creates a Handler backed by the given DB.
func New(db *storage.DB, maxMsgPerSession int) *Handler {
	return &Handler{db: db, maxMsgPerSession: maxMsgPerSession, extractionPersona: "brains"}
}

// SetActivityLog attaches the activity log for operation tracking.
func (h *Handler) SetActivityLog(log *ActivityLog) {
	h.activity = log
}

// ActivityEntries returns the current activity log entries for schema display.
func (h *Handler) ActivityEntries() []ActivityEntry {
	if h.activity == nil {
		return nil
	}
	return h.activity.Entries()
}

// SetSDK sets the SDK client for inter-plugin communication.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// SetCompactor attaches the compaction buffer to this handler.
func (h *Handler) SetCompactor(c *Compactor) {
	h.compactor = c
}

// SetExtractionPersona sets the persona alias used for memory extraction.
func (h *Handler) SetExtractionPersona(persona string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.extractionPersona = persona
}

func (h *Handler) getExtractionPersona() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.extractionPersona
}

// lookupMemoryPersona finds the persona with the "memory" role via the persona plugin,
// falling back to the configured extraction persona if the role lookup fails.
func (h *Handler) lookupMemoryPersona() string {
	if h.sdk != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		respBody, err := h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "GET", "/personas/by-role/memory", nil)
		if err == nil {
			var resp struct {
				Personas []struct {
					Alias string `json:"alias"`
				} `json:"personas"`
			}
			if json.Unmarshal(respBody, &resp) == nil && len(resp.Personas) > 0 {
				return resp.Personas[0].Alias
			}
		}
	}
	return h.getExtractionPersona()
}

// ValidMemoryCategories lists acceptable categories for memory facts.
var ValidMemoryCategories = map[string]bool{
	"user_fact": true,
	"decision":  true,
	"project":   true,
	"reference": true,
	"general":   true,
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
	// Push to compaction buffer.
	if h.compactor != nil {
		h.compactor.Push(sessionID, req.Role, req.Content, req.Responder)
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

// ToolDefs returns all tool definitions (session + memory) as a plain slice for schema display.
func (h *Handler) ToolDefs() interface{} {
	sessionTools := []gin.H{
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
	}
	return append(sessionTools, h.MemoryTools()...)
}

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
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
	// Push to compaction buffer.
	if h.compactor != nil {
		h.compactor.Push(req.SessionID, req.Role, req.Content, req.Responder)
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

// ── Memory (facts) MCP tools ─────────────────────────────────────────────────

// MemoryTools returns tool definitions for the memory fact system.
func (h *Handler) MemoryTools() []gin.H {
	categories := make([]string, 0, len(ValidMemoryCategories))
	for k := range ValidMemoryCategories {
		categories = append(categories, k)
	}

	return []gin.H{
		{
			"name":        "recall_memory",
			"description": "Search stored memories by keyword query. Returns facts ranked by relevance and importance. Use this to recall context about users, past decisions, projects, or any stored knowledge.",
			"endpoint":    "/mcp/recall_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"query":    gin.H{"type": "string", "description": "Search query — keywords to match against memory content and tags"},
					"category": gin.H{"type": "string", "enum": categories, "description": "Optional category filter"},
					"limit":    gin.H{"type": "integer", "description": "Max results to return (default: 10)"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "save_memory",
			"description": "Store an important fact, decision, or piece of context for future recall. Use this when you encounter information worth remembering across conversations — user preferences, technical decisions, project context, or reference pointers.",
			"endpoint":    "/mcp/save_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"content":    gin.H{"type": "string", "description": "The fact or information to remember"},
					"category":   gin.H{"type": "string", "enum": categories, "description": "Category: user_fact, decision, project, reference, or general"},
					"tags":       gin.H{"type": "string", "description": "Comma-separated tags for searchability (e.g. 'api,migration,postgres')"},
					"importance": gin.H{"type": "integer", "description": "1-10 importance score (default: 5, use 8+ for critical facts)"},
				},
				"required": []string{"content", "category"},
			},
		},
		{
			"name":        "update_memory",
			"description": "Update an existing memory — change its content, tags, category, or importance. Use when a previously stored fact has changed or needs correction.",
			"endpoint":    "/mcp/update_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"id":         gin.H{"type": "integer", "description": "Memory ID to update"},
					"content":    gin.H{"type": "string", "description": "Updated content"},
					"category":   gin.H{"type": "string", "enum": categories, "description": "Updated category"},
					"tags":       gin.H{"type": "string", "description": "Updated tags"},
					"importance": gin.H{"type": "integer", "description": "Updated importance (1-10)"},
				},
				"required": []string{"id"},
			},
		},
		{
			"name":        "delete_memory",
			"description": "Remove an outdated or incorrect memory by ID.",
			"endpoint":    "/mcp/delete_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"id": gin.H{"type": "integer", "description": "Memory ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		{
			"name":        "list_memories",
			"description": "List all stored memories, optionally filtered by category. Returns memories ordered by importance then recency.",
			"endpoint":    "/mcp/list_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"category": gin.H{"type": "string", "enum": categories, "description": "Optional category filter"},
					"limit":    gin.H{"type": "integer", "description": "Max results (default: 50)"},
				},
			},
		},
		{
			"name":        "compact_conversation",
			"description": "Send a conversation transcript for fact extraction. An extraction agent will analyze the transcript and save important facts, decisions, and context.",
			"endpoint":    "/mcp/compact_conversation",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"session_id":  gin.H{"type": "string", "description": "Session ID for the conversation being compacted"},
					"transcript":  gin.H{"type": "string", "description": "The full conversation transcript text"},
					"agent_alias": gin.H{"type": "string", "description": "The agent alias that participated in the conversation (optional)"},
				},
				"required": []string{"session_id", "transcript"},
			},
		},
	}
}

// MCPRecallMemory handles POST /mcp/recall_memory — keyword search across stored facts.
func (h *Handler) MCPRecallMemory(c *gin.Context) {
	var req struct {
		Query    string `json:"query" binding:"required"`
		Category string `json:"category"`
		Limit    int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Category != "" && !ValidMemoryCategories[req.Category] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category"})
		return
	}
	memories, err := h.db.RecallMemory(req.Query, req.Category, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.activity != nil {
		h.activity.RecordRecall(req.Query, "", len(memories))
	}
	c.JSON(http.StatusOK, gin.H{"memories": memories, "count": len(memories)})
}

// MCPSaveMemory handles POST /mcp/save_memory — store a new fact.
func (h *Handler) MCPSaveMemory(c *gin.Context) {
	var req struct {
		Content       string `json:"content" binding:"required"`
		Category      string `json:"category" binding:"required"`
		Tags          string `json:"tags"`
		Importance    int    `json:"importance"`
		SourceAgent   string `json:"source_agent"`
		SourceSession string `json:"source_session"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !ValidMemoryCategories[req.Category] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category, must be one of: user_fact, decision, project, reference, general"})
		return
	}
	// Allow source_agent from header if not in body.
	if req.SourceAgent == "" {
		req.SourceAgent = c.GetHeader("X-Teamagentica-Agent-Alias")
	}
	if req.SourceSession == "" {
		req.SourceSession = c.GetHeader("X-Teamagentica-Session-ID")
	}
	if req.Importance == 0 {
		req.Importance = 5
	}
	mem, err := h.db.SaveMemory(req.Category, req.Content, req.Tags, req.SourceAgent, req.SourceSession, req.Importance)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.activity != nil {
		h.activity.RecordSave(req.Category, req.Content, req.SourceAgent, req.SourceSession)
	}
	c.JSON(http.StatusCreated, gin.H{"memory": mem})
}

// MCPUpdateMemory handles POST /mcp/update_memory — modify an existing fact.
func (h *Handler) MCPUpdateMemory(c *gin.Context) {
	var req struct {
		ID         uint   `json:"id" binding:"required"`
		Content    string `json:"content"`
		Category   string `json:"category"`
		Tags       string `json:"tags"`
		Importance int    `json:"importance"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Category != "" && !ValidMemoryCategories[req.Category] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category"})
		return
	}
	updates := map[string]interface{}{}
	if req.Content != "" {
		updates["content"] = req.Content
	}
	if req.Category != "" {
		updates["category"] = req.Category
	}
	if req.Tags != "" {
		updates["tags"] = req.Tags
	}
	if req.Importance > 0 {
		updates["importance"] = req.Importance
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}
	mem, err := h.db.UpdateMemory(req.ID, updates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.activity != nil {
		h.activity.RecordUpdate(req.ID, "")
	}
	c.JSON(http.StatusOK, gin.H{"memory": mem})
}

// MCPDeleteMemory handles POST /mcp/delete_memory — remove a fact.
func (h *Handler) MCPDeleteMemory(c *gin.Context) {
	var req struct {
		ID uint `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.DeleteMemory(req.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.activity != nil {
		h.activity.RecordDelete(req.ID)
	}
	c.JSON(http.StatusOK, gin.H{"message": "memory deleted"})
}

// MCPListMemories handles POST /mcp/list_memories — browse stored facts.
func (h *Handler) MCPListMemories(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
		Limit    int    `json:"limit"`
	}
	// Allow empty body for listing all.
	_ = c.ShouldBindJSON(&req)
	if req.Category != "" && !ValidMemoryCategories[req.Category] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category"})
		return
	}
	memories, err := h.db.ListMemories(req.Category, req.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"memories": memories, "count": len(memories)})
}

// ── Compact (fact extraction) ─────────────────────────────────────────────────

// extractedFact is a single fact returned by the extraction persona.
type extractedFact struct {
	Content    string `json:"content"`
	Category   string `json:"category"`
	Tags       string `json:"tags"`
	Importance int    `json:"importance"`
}

// extractionResponse is the JSON envelope returned by @brains.
type extractionResponse struct {
	Facts []extractedFact `json:"facts"`
}

// MCPCompact handles POST /mcp/compact_conversation and POST /compact.
// It sends the transcript to an extraction persona via the relay and stores the resulting facts.
func (h *Handler) MCPCompact(c *gin.Context) {
	var req struct {
		SessionID  string `json:"session_id" binding:"required"`
		Transcript string `json:"transcript" binding:"required"`
		AgentAlias string `json:"agent_alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.sdk == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "SDK client not configured"})
		return
	}

	persona := h.lookupMemoryPersona()

	// Build the extraction prompt addressed to the persona.
	extractionPrompt := fmt.Sprintf(`@%s Extract important facts from this conversation transcript. Return ONLY valid JSON in this format:
{"facts": [{"content": "the fact", "category": "user_fact|decision|project|reference|general", "tags": "comma,separated,tags", "importance": 5}]}

Categories:
- user_fact: User preferences, roles, knowledge
- decision: Technical or project decisions made
- project: Project context, goals, status
- reference: Pointers to external resources
- general: Other important information

Transcript:
---
%s
---`, persona, req.Transcript)

	// Build the relay request matching relayRequest struct.
	relayReq := map[string]interface{}{
		"source_plugin": "infra-agent-memory",
		"channel_id":    "compact:" + req.SessionID,
		"message":       extractionPrompt,
	}
	body, err := json.Marshal(relayReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal relay request"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	respBody, err := h.sdk.RouteToPlugin(ctx, "infra-agent-relay", "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		log.Printf("[compact] relay call failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "relay call failed: " + err.Error()})
		return
	}

	// The relay returns {"task_group_id": "..."} for async processing.
	// Parse and return it — the extraction will be processed asynchronously
	// and facts will arrive via relay:progress events.
	var relayResp struct {
		TaskGroupID string `json:"task_group_id"`
		Response    string `json:"response,omitempty"`
	}
	if err := json.Unmarshal(respBody, &relayResp); err != nil {
		log.Printf("[compact] failed to parse relay response: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid relay response"})
		return
	}

	// If the relay returned a synchronous response (e.g. workspace routing),
	// try to parse facts from it immediately.
	if relayResp.Response != "" {
		stored := h.parseAndStoreFacts(relayResp.Response, req.AgentAlias, req.SessionID)
		if h.activity != nil {
			h.activity.RecordCompactResult(req.SessionID, len(stored))
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "completed",
			"facts":   stored,
			"count":   len(stored),
		})
		return
	}

	// Async — return the task group ID so caller can track progress.
	if h.activity != nil {
		h.activity.RecordCompactDispatch(req.SessionID, 0)
	}
	c.JSON(http.StatusAccepted, gin.H{
		"status":         "dispatched",
		"task_group_id":  relayResp.TaskGroupID,
		"message":        fmt.Sprintf("Extraction dispatched to @%s — facts will be stored when processing completes", persona),
	})
}

// compactTranscript is called by the Compactor to send a transcript for extraction.
// It reuses the same relay call logic as MCPCompact but runs outside an HTTP context.
func (h *Handler) compactTranscript(sessionID, transcript, agentAlias string) {
	if h.sdk == nil {
		log.Printf("[compactor] SDK not configured, skipping compaction for session=%s", sessionID)
		return
	}

	persona := h.lookupMemoryPersona()

	extractionPrompt := fmt.Sprintf(`@%s Extract important facts from this conversation transcript. Return ONLY valid JSON in this format:
{"facts": [{"content": "the fact", "category": "user_fact|decision|project|reference|general", "tags": "comma,separated,tags", "importance": 5}]}

Categories:
- user_fact: User preferences, roles, knowledge
- decision: Technical or project decisions made
- project: Project context, goals, status
- reference: Pointers to external resources
- general: Other important information

Transcript:
---
%s
---`, persona, transcript)

	relayReq := map[string]interface{}{
		"source_plugin": "infra-agent-memory",
		"channel_id":    "compact:" + sessionID,
		"message":       extractionPrompt,
	}
	body, err := json.Marshal(relayReq)
	if err != nil {
		log.Printf("[compactor] failed to marshal relay request: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	respBody, err := h.sdk.RouteToPlugin(ctx, "infra-agent-relay", "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		log.Printf("[compactor] relay call failed for session=%s: %v", sessionID, err)
		return
	}

	var relayResp struct {
		TaskGroupID string `json:"task_group_id"`
		Response    string `json:"response,omitempty"`
	}
	if err := json.Unmarshal(respBody, &relayResp); err != nil {
		log.Printf("[compactor] failed to parse relay response for session=%s: %v", sessionID, err)
		return
	}

	if relayResp.Response != "" {
		stored := h.parseAndStoreFacts(relayResp.Response, agentAlias, sessionID)
		if h.activity != nil {
			h.activity.RecordCompactResult(sessionID, len(stored))
		}
	} else {
		if h.activity != nil {
			h.activity.RecordCompactDispatch(sessionID, 0)
		}
		log.Printf("[compactor] extraction dispatched async for session=%s task_group=%s", sessionID, relayResp.TaskGroupID)
	}
}

// parseAndStoreFacts extracts JSON facts from an agent response and saves them to the DB.
func (h *Handler) parseAndStoreFacts(response, agentAlias, sessionID string) []*storage.Memory {
	// Try to find JSON in the response — the agent may wrap it in markdown code blocks.
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		log.Printf("[compact] no JSON found in extraction response")
		return nil
	}

	var extraction extractionResponse
	if err := json.Unmarshal([]byte(jsonStr), &extraction); err != nil {
		log.Printf("[compact] failed to parse extraction JSON: %v", err)
		return nil
	}

	var stored []*storage.Memory
	for _, fact := range extraction.Facts {
		if fact.Content == "" {
			continue
		}
		if !ValidMemoryCategories[fact.Category] {
			fact.Category = "general"
		}
		if fact.Importance < 1 || fact.Importance > 10 {
			fact.Importance = 5
		}
		mem, err := h.db.SaveMemory(fact.Category, fact.Content, fact.Tags, agentAlias, sessionID, fact.Importance)
		if err != nil {
			log.Printf("[compact] failed to save fact: %v", err)
			continue
		}
		stored = append(stored, mem)
	}
	log.Printf("[compact] stored %d facts from extraction (session=%s)", len(stored), sessionID)
	return stored
}

// ParseAndStoreFacts is the exported version for use by event handlers.
func (h *Handler) ParseAndStoreFacts(response, agentAlias, sessionID string) []*storage.Memory {
	return h.parseAndStoreFacts(response, agentAlias, sessionID)
}

// extractJSON tries to find a JSON object in the response text.
// Handles raw JSON and markdown code blocks.
func extractJSON(s string) string {
	// Try raw parse first.
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return s
	}

	// Look for ```json ... ``` blocks.
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}

	// Look for ``` ... ``` blocks.
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(s[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}

	// Look for first { ... } span.
	if idx := strings.Index(s, "{"); idx >= 0 {
		depth := 0
		for i := idx; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}

	return ""
}
