package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/storage"
)

// Handler serves persona API endpoints.
type Handler struct {
	db                       *storage.DB
	workerPrompt      string
	coordinatorPrompt string
}

// New creates a new Handler.
func New(db *storage.DB, workerPrompt, coordinatorPrompt string) *Handler {
	return &Handler{db: db, workerPrompt: workerPrompt, coordinatorPrompt: coordinatorPrompt}
}

// Health handles GET /health.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ListPersonas handles GET /personas — returns all personas.
func (h *Handler) ListPersonas(c *gin.Context) {
	personas, err := h.db.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"personas": personas})
}

// GetPersona handles GET /personas/:alias — returns a single persona.
func (h *Handler) GetPersona(c *gin.Context) {
	alias := c.Param("alias")
	p, err := h.db.Get(alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "persona not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// CreatePersona handles POST /personas — creates a new persona.
func (h *Handler) CreatePersona(c *gin.Context) {
	var req storage.Persona
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if req.SystemPrompt == "" {
		req.SystemPrompt = h.defaultPromptForAlias(req.Alias)
	}
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	c.JSON(http.StatusCreated, req)
}

// UpdatePersona handles PUT /personas/:alias — updates an existing persona.
func (h *Handler) UpdatePersona(c *gin.Context) {
	alias := c.Param("alias")

	// Verify it exists.
	if _, err := h.db.Get(alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "persona not found"})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Only allow updating known fields.
	updates := make(map[string]interface{})
	for _, key := range []string{"system_prompt", "model", "backend_alias"} {
		if v, ok := body[key]; ok {
			updates[key] = v
		}
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid fields to update"})
		return
	}

	if err := h.db.Update(alias, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	p, _ := h.db.Get(alias)
	c.JSON(http.StatusOK, p)
}

// defaultPromptForAlias returns the appropriate default prompt based on the persona alias.
func (h *Handler) defaultPromptForAlias(alias string) string {
	if alias == "coordinator" || alias == "default-coordinator" {
		return h.coordinatorPrompt
	}
	return h.workerPrompt
}

// DefaultPrompt returns the default worker system prompt for pre-filling new persona forms.
func (h *Handler) DefaultPrompt(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"system_prompt": h.workerPrompt})
}

// DeletePersona handles DELETE /personas/:alias.
func (h *Handler) DeletePersona(c *gin.Context) {
	alias := c.Param("alias")
	if err := h.db.Delete(alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GetTools handles GET /tools — returns MCP tool definitions.
func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tools": []gin.H{
			{
				"name":        "list_personas",
				"description": "List all configured agent personas",
				"endpoint":    "/mcp/list_personas",
				"parameters":  gin.H{"type": "object", "properties": gin.H{}},
			},
			{
				"name":        "get_persona",
				"description": "Get a specific agent persona by alias",
				"endpoint":    "/mcp/get_persona",
				"parameters": gin.H{
					"type":       "object",
					"properties": gin.H{"alias": gin.H{"type": "string", "description": "Persona alias"}},
					"required":   []string{"alias"},
				},
			},
			{
				"name":        "create_persona",
				"description": "Create a new agent persona with a system prompt",
				"endpoint":    "/mcp/create_persona",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"alias":         gin.H{"type": "string", "description": "Unique alias for this persona"},
						"system_prompt": gin.H{"type": "string", "description": "System prompt defining the persona's behavior"},
						"model":         gin.H{"type": "string", "description": "Optional model override"},
						"backend_alias": gin.H{"type": "string", "description": "Backend agent alias to route to (e.g. claude, codex)"},
					},
					"required": []string{"alias", "system_prompt"},
				},
			},
			{
				"name":        "update_persona",
				"description": "Update an existing agent persona",
				"endpoint":    "/mcp/update_persona",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"alias":         gin.H{"type": "string", "description": "Persona alias to update"},
						"system_prompt": gin.H{"type": "string", "description": "New system prompt"},
						"model":         gin.H{"type": "string", "description": "New model override"},
						"backend_alias": gin.H{"type": "string", "description": "New backend agent alias"},
					},
					"required": []string{"alias"},
				},
			},
			{
				"name":        "delete_persona",
				"description": "Delete an agent persona",
				"endpoint":    "/mcp/delete_persona",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{"alias": gin.H{"type": "string", "description": "Persona alias to delete"}},
					"required":   []string{"alias"},
				},
			},
		},
	})
}

// MCPListPersonas handles POST /mcp/list_personas.
func (h *Handler) MCPListPersonas(c *gin.Context) {
	personas, err := h.db.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"personas": personas})
}

// MCPGetPersona handles POST /mcp/get_persona.
func (h *Handler) MCPGetPersona(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	p, err := h.db.Get(req.Alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "persona not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// MCPCreatePersona handles POST /mcp/create_persona.
func (h *Handler) MCPCreatePersona(c *gin.Context) {
	var req storage.Persona
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if req.SystemPrompt == "" {
		req.SystemPrompt = h.defaultPromptForAlias(req.Alias)
	}
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	c.JSON(http.StatusCreated, req)
}

// MCPUpdatePersona handles POST /mcp/update_persona.
func (h *Handler) MCPUpdatePersona(c *gin.Context) {
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	alias, _ := body["alias"].(string)
	if alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if _, err := h.db.Get(alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "persona not found"})
		return
	}
	updates := make(map[string]interface{})
	for _, key := range []string{"system_prompt", "model", "backend_alias"} {
		if v, ok := body[key]; ok {
			updates[key] = v
		}
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid fields to update"})
		return
	}
	if err := h.db.Update(alias, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(alias)
	c.JSON(http.StatusOK, p)
}

// MCPDeletePersona handles POST /mcp/delete_persona.
func (h *Handler) MCPDeletePersona(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if err := h.db.Delete(req.Alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
