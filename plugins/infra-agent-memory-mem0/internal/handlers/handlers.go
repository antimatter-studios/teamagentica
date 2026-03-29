package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory-mem0/internal/memory"
)

// Handler serves REST + MCP endpoints backed by a memory Provider.
type Handler struct {
	provider memory.Provider
	mem0URL  string // base URL of the local Mem0 Python server
}

// New creates a Handler backed by the given memory provider.
func New(provider memory.Provider) *Handler {
	return &Handler{provider: provider, mem0URL: "http://localhost:8010"}
}

// SetMem0URL sets the base URL for the local Mem0 Python server.
func (h *Handler) SetMem0URL(url string) {
	h.mem0URL = url
}

// Health checks if the memory backend is reachable.
func (h *Handler) Health(c *gin.Context) {
	if err := h.provider.Healthy(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// ── Schema helpers ──────────────────────────────────────────────────────────────

// schemaCtx returns a context with a short timeout for schema polling calls.
func schemaCtx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second) //nolint:lostcancel
	return ctx
}

// MemoryList returns stored memories for the schema readonly section.
// Returns (items, totalCount) so callers can show "Memories (50/1234)".
func (h *Handler) MemoryList() (interface{}, int, int) {
	mems, err := h.provider.List(schemaCtx(), memory.ListOpts{
		Filters:  map[string]any{"user_id": "global"},
		PageSize: 50,
	})
	if err != nil {
		log.Printf("schema memory list error: %v", err)
		return nil, 0, 0
	}
	// Get total count from stats (uses page_size=1000).
	allMems, _ := h.provider.List(schemaCtx(), memory.ListOpts{
		Filters:  map[string]any{"user_id": "global"},
		PageSize: 1000,
	})
	total := len(allMems)
	// Sort memories by created_at descending (newest first).
	sort.Slice(mems, func(i, j int) bool {
		return mems[i].CreatedAt > mems[j].CreatedAt
	})

	type item struct {
		Time    string   `json:"time"`
		Message string   `json:"message"`
		Summary string   `json:"summary"`
		ID      string   `json:"id"`
		Text    string   `json:"text"`
		UserID  string   `json:"user_id,omitempty"`
		AgentID string   `json:"agent_id,omitempty"`
		Tags    []string `json:"categories,omitempty"`
	}
	result := make([]item, len(mems))
	for i, m := range mems {
		text := m.Text
		if len(text) > 120 {
			text = text[:117] + "..."
		}
		// Format datetime for display: "2026-03-27 14:30"
		displayTime := m.CreatedAt
		if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
			displayTime = t.Format("2006-01-02 15:04")
		} else if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
			displayTime = t.Format("2006-01-02 15:04")
		}
		result[i] = item{
			Time:    displayTime,
			Message: text,
			Summary: m.ID,
			ID:      m.ID,
			Text:    m.Text,
			UserID:  m.UserID,
			AgentID: m.AgentID,
			Tags:    m.Categories,
		}
	}
	return result, total, len(result)
}

// MemoryStats returns aggregate counts for the schema readonly section.
func (h *Handler) MemoryStats() interface{} {
	mems, err := h.provider.List(schemaCtx(), memory.ListOpts{
		Filters:  map[string]any{"user_id": "global"},
		PageSize: 1000,
	})
	if err != nil {
		log.Printf("schema memory stats error: %v", err)
		return nil
	}
	entities, _ := h.provider.ListEntities(schemaCtx())

	stats := map[string]any{
		"total_memories": len(mems),
		"total_entities": len(entities),
	}

	// Poll Mem0's migration/sync status.
	if migration := h.fetchMigrationStatus(); migration != nil {
		stats["sync"] = migration
	}

	return stats
}

// fetchMigrationStatus polls the Mem0 Python server for background sync progress.
func (h *Handler) fetchMigrationStatus() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", h.mem0URL+"/migration/status", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var status struct {
		Active    bool `json:"active"`
		Processed int  `json:"processed"`
		Total     int  `json:"total"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return nil
	}

	if !status.Active {
		return nil
	}

	pct := 0
	if status.Total > 0 {
		pct = (status.Processed * 100) / status.Total
	}
	return map[string]any{
		"status":    fmt.Sprintf("Re-indexing: %d/%d memories (%d%%)", status.Processed, status.Total, pct),
		"processed": status.Processed,
		"total":     status.Total,
	}
}

// EntityList returns known entities for the schema readonly section.
func (h *Handler) EntityList() interface{} {
	entities, err := h.provider.ListEntities(schemaCtx())
	if err != nil {
		log.Printf("schema entity list error: %v", err)
		return nil
	}
	type item struct {
		Time    string `json:"time"`
		Message string `json:"message"`
		Summary string `json:"summary"`
		Type    string `json:"type"`
		ID      string `json:"id"`
	}
	result := make([]item, len(entities))
	for i, e := range entities {
		result[i] = item{
			Time:    e.Type,
			Message: e.ID,
			Summary: e.Type,
			Type:    e.Type,
			ID:      e.ID,
		}
	}
	return result
}

// ── MCP Tool Definitions ────────────────────────────────────────────────────────

func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "add_memory",
			"description": "Save a conversation or text to memory. Mem0 automatically extracts important facts. Use this to remember conversations, decisions, or important context.",
			"endpoint":    "/mcp/add_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"messages": gin.H{
						"type":        "array",
						"description": "Array of {role, content} message objects to extract memories from",
						"items": gin.H{
							"type": "object",
							"properties": gin.H{
								"role":    gin.H{"type": "string", "description": "Message role: user or assistant"},
								"content": gin.H{"type": "string", "description": "Message content"},
							},
							"required": []string{"role", "content"},
						},
					},
					"user_id":              gin.H{"type": "string", "description": "User scope (default: global)"},
					"agent_id":             gin.H{"type": "string", "description": "Agent scope"},
					"run_id":               gin.H{"type": "string", "description": "Run/session scope"},
					"metadata":             gin.H{"type": "object", "description": "Arbitrary metadata to attach"},
					"infer":                gin.H{"type": "boolean", "description": "Extract facts from messages (default: true)"},
					"immutable":            gin.H{"type": "boolean", "description": "If true, memory cannot be updated later"},
					"custom_categories":    gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Constrain extraction to these categories"},
					"custom_instructions":  gin.H{"type": "string", "description": "Extra guidance for the extraction LLM"},
				},
				"required": []string{"messages"},
			},
		},
		{
			"name":        "search_memories",
			"description": "Semantic search across stored memories. Returns the most relevant memories for a query.",
			"endpoint":    "/mcp/search_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"query":          gin.H{"type": "string", "description": "Natural language search query"},
					"user_id":        gin.H{"type": "string", "description": "Filter by user (default: global)"},
					"agent_id":       gin.H{"type": "string", "description": "Filter by agent"},
					"run_id":         gin.H{"type": "string", "description": "Filter by run/session"},
					"top_k":          gin.H{"type": "integer", "description": "Max results to return (default: 10)"},
					"threshold":      gin.H{"type": "number", "description": "Minimum similarity score (0-1)"},
					"rerank":         gin.H{"type": "boolean", "description": "Re-rank results for better relevance"},
					"keyword_search": gin.H{"type": "boolean", "description": "Also include keyword-based matches"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_memories",
			"description": "List all memories with optional filters and pagination.",
			"endpoint":    "/mcp/get_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id":   gin.H{"type": "string", "description": "Filter by user (default: global)"},
					"agent_id":  gin.H{"type": "string", "description": "Filter by agent"},
					"run_id":    gin.H{"type": "string", "description": "Filter by run/session"},
					"page":      gin.H{"type": "integer", "description": "Page number for pagination"},
					"page_size": gin.H{"type": "integer", "description": "Results per page (default: 50)"},
				},
			},
		},
		{
			"name":        "get_memory",
			"description": "Retrieve a single memory by its ID.",
			"endpoint":    "/mcp/get_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to retrieve"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "update_memory",
			"description": "Update an existing memory's text or metadata.",
			"endpoint":    "/mcp/update_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to update"},
					"text":      gin.H{"type": "string", "description": "New text content"},
					"metadata":  gin.H{"type": "object", "description": "Updated metadata"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "delete_memory",
			"description": "Delete a single memory by ID.",
			"endpoint":    "/mcp/delete_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to delete"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "delete_all_memories",
			"description": "Delete all memories for a given scope (user, agent, app, or run). At least one scope filter is required.",
			"endpoint":    "/mcp/delete_all_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id":  gin.H{"type": "string", "description": "Delete all memories for this user"},
					"agent_id": gin.H{"type": "string", "description": "Delete all memories for this agent"},
					"app_id":   gin.H{"type": "string", "description": "Delete all memories for this app"},
					"run_id":   gin.H{"type": "string", "description": "Delete all memories for this run"},
				},
			},
		},
		{
			"name":        "delete_entities",
			"description": "Hard-delete an entity (user, agent, app, or run) and all its associated memories.",
			"endpoint":    "/mcp/delete_entities",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"entity_type": gin.H{"type": "string", "enum": []string{"user", "agent", "app", "run"}, "description": "Entity type"},
					"entity_id":   gin.H{"type": "string", "description": "Entity ID to delete"},
				},
				"required": []string{"entity_type", "entity_id"},
			},
		},
		{
			"name":        "list_entities",
			"description": "List all known entities (users, agents, apps, runs) in the memory system.",
			"endpoint":    "/mcp/list_entities",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
	}
}

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// ── MCP Tool Execution ──────────────────────────────────────────────────────────

func (h *Handler) MCPAddMemory(c *gin.Context) {
	var req struct {
		Messages           []memory.Message `json:"messages"`
		UserID             string           `json:"user_id"`
		AgentID            string           `json:"agent_id"`
		AppID              string           `json:"app_id"`
		RunID              string           `json:"run_id"`
		Metadata           map[string]any   `json:"metadata"`
		Infer              *bool            `json:"infer"`
		Immutable          *bool            `json:"immutable"`
		EnableGraph        *bool            `json:"enable_graph"`
		ExpirationDate     string           `json:"expiration_date"`
		CustomCategories   []string         `json:"custom_categories"`
		CustomInstructions string           `json:"custom_instructions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages is required"})
		return
	}

	userID := req.UserID
	if userID == "" {
		userID = "global"
	}

	memories, err := h.provider.Add(c.Request.Context(), req.Messages, memory.AddOpts{
		UserID:             userID,
		AgentID:            req.AgentID,
		AppID:              req.AppID,
		RunID:              req.RunID,
		Metadata:           req.Metadata,
		Infer:              req.Infer,
		Immutable:          req.Immutable,
		EnableGraph:        req.EnableGraph,
		ExpirationDate:     req.ExpirationDate,
		CustomCategories:   req.CustomCategories,
		CustomInstructions: req.CustomInstructions,
	})
	if err != nil {
		log.Printf("add_memory error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": memories})
}

func (h *Handler) MCPSearchMemories(c *gin.Context) {
	var req struct {
		Query         string  `json:"query"`
		UserID        string  `json:"user_id"`
		AgentID       string  `json:"agent_id"`
		RunID         string  `json:"run_id"`
		TopK          int     `json:"top_k"`
		Threshold     float64 `json:"threshold"`
		Rerank        *bool   `json:"rerank"`
		KeywordSearch *bool   `json:"keyword_search"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	filters := map[string]any{}
	if req.UserID != "" {
		filters["user_id"] = req.UserID
	} else {
		filters["user_id"] = "global"
	}
	if req.AgentID != "" {
		filters["agent_id"] = req.AgentID
	}
	if req.RunID != "" {
		filters["run_id"] = req.RunID
	}

	memories, err := h.provider.Search(c.Request.Context(), req.Query, memory.SearchOpts{
		Filters:       filters,
		TopK:          req.TopK,
		Threshold:     req.Threshold,
		Rerank:        req.Rerank,
		KeywordSearch: req.KeywordSearch,
	})
	if err != nil {
		log.Printf("search_memories error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": memories})
}

func (h *Handler) MCPGetMemories(c *gin.Context) {
	var req struct {
		UserID   string `json:"user_id"`
		AgentID  string `json:"agent_id"`
		RunID    string `json:"run_id"`
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filters := map[string]any{}
	if req.UserID != "" {
		filters["user_id"] = req.UserID
	} else {
		filters["user_id"] = "global"
	}
	if req.AgentID != "" {
		filters["agent_id"] = req.AgentID
	}
	if req.RunID != "" {
		filters["run_id"] = req.RunID
	}

	memories, err := h.provider.List(c.Request.Context(), memory.ListOpts{
		Filters:  filters,
		Page:     req.Page,
		PageSize: req.PageSize,
	})
	if err != nil {
		log.Printf("get_memories error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Fetch total count for pagination display (e.g. "100/847").
	total := len(memories)
	type counter interface {
		Count(ctx context.Context, filters map[string]any) (int, error)
	}
	if cp, ok := h.provider.(counter); ok {
		if n, err := cp.Count(c.Request.Context(), filters); err == nil && n > 0 {
			total = n
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"results": memories,
		"total":   total,
	})
}

func (h *Handler) MCPGetMemory(c *gin.Context) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MemoryID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "memory_id is required"})
		return
	}

	mem, err := h.provider.Get(c.Request.Context(), req.MemoryID)
	if err != nil {
		log.Printf("get_memory error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, mem)
}

func (h *Handler) MCPUpdateMemory(c *gin.Context) {
	var req struct {
		MemoryID string         `json:"memory_id"`
		Text     string         `json:"text"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MemoryID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "memory_id is required"})
		return
	}

	if err := h.provider.Update(c.Request.Context(), req.MemoryID, req.Text, req.Metadata); err != nil {
		log.Printf("update_memory error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *Handler) MCPDeleteMemory(c *gin.Context) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MemoryID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "memory_id is required"})
		return
	}

	if err := h.provider.Delete(c.Request.Context(), req.MemoryID); err != nil {
		log.Printf("delete_memory error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *Handler) MCPDeleteAllMemories(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id"`
		AgentID string `json:"agent_id"`
		AppID   string `json:"app_id"`
		RunID   string `json:"run_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.UserID == "" && req.AgentID == "" && req.AppID == "" && req.RunID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one of user_id, agent_id, app_id, or run_id is required"})
		return
	}

	filters := map[string]any{}
	if req.UserID != "" {
		filters["user_id"] = req.UserID
	}
	if req.AgentID != "" {
		filters["agent_id"] = req.AgentID
	}
	if req.AppID != "" {
		filters["app_id"] = req.AppID
	}
	if req.RunID != "" {
		filters["run_id"] = req.RunID
	}

	if err := h.provider.DeleteAll(c.Request.Context(), filters); err != nil {
		log.Printf("delete_all_memories error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *Handler) MCPDeleteEntities(c *gin.Context) {
	var req struct {
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.EntityType == "" || req.EntityID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entity_type and entity_id are required"})
		return
	}

	if err := h.provider.DeleteEntities(c.Request.Context(), req.EntityType, req.EntityID); err != nil {
		log.Printf("delete_entities error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *Handler) MCPListEntities(c *gin.Context) {
	entities, err := h.provider.ListEntities(c.Request.Context())
	if err != nil {
		log.Printf("list_entities error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": entities})
}
