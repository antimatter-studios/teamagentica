package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/storage"
)

// Handler serves persona API endpoints.
type Handler struct {
	db                *storage.DB
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

// --- Persona REST endpoints ---

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
	if req.Role == "" {
		req.Role = "worker"
	}
	// Auto-fill system_prompt from role default if not provided.
	if req.SystemPrompt == "" {
		if rolePrompt := h.db.GetRolePrompt(req.Role); rolePrompt != "" {
			req.SystemPrompt = rolePrompt
		} else {
			req.SystemPrompt = h.defaultPromptForAlias(req.Alias)
		}
	}
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	// If a non-worker role was specified, use AssignRole for trim logic.
	if req.Role != "worker" {
		if err := h.db.AssignRole(req.Alias, req.Role); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	p, _ := h.db.Get(req.Alias)
	c.JSON(http.StatusCreated, p)
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

	// Handle role change via AssignRole.
	if roleVal, ok := body["role"]; ok {
		if roleStr, ok := roleVal.(string); ok && roleStr != "" {
			if err := h.db.AssignRole(alias, roleStr); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
	}

	// Only allow updating known fields.
	updates := make(map[string]interface{})
	for _, key := range []string{"system_prompt", "model", "backend_alias"} {
		if v, ok := body[key]; ok {
			updates[key] = v
		}
	}
	if len(updates) > 0 {
		if err := h.db.Update(alias, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
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

// GetPersonasByRole handles GET /personas/by-role/:role.
func (h *Handler) GetPersonasByRole(c *gin.Context) {
	role := c.Param("role")
	personas, err := h.db.GetPersonasByRole(role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"personas": personas})
}

// --- Role REST endpoints ---

// ListRoles handles GET /roles.
func (h *Handler) ListRoles(c *gin.Context) {
	roles, err := h.db.ListRoles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

// GetRole handles GET /roles/:id.
func (h *Handler) GetRole(c *gin.Context) {
	id := c.Param("id")
	r, err := h.db.GetRole(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// CreateRole handles POST /roles.
func (h *Handler) CreateRole(c *gin.Context) {
	var req storage.Role
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label is required"})
		return
	}
	if err := h.db.CreateRole(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "role already exists"})
		return
	}
	c.JSON(http.StatusCreated, req)
}

// UpdateRole handles PUT /roles/:id.
func (h *Handler) UpdateRole(c *gin.Context) {
	id := c.Param("id")

	if _, err := h.db.GetRole(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := make(map[string]interface{})
	if v, ok := body["label"]; ok {
		updates["label"] = v
	}
	if v, ok := body["max_count"]; ok {
		updates["max_count"] = v
	}
	if v, ok := body["system_prompt"]; ok {
		updates["system_prompt"] = v
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid fields to update"})
		return
	}

	r, err := h.db.UpdateRole(id, updates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// DeleteRole handles DELETE /roles/:id.
func (h *Handler) DeleteRole(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.DeleteRole(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// --- MCP tool definitions ---

// ToolDefs returns the MCP tool definitions as a generic interface.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
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
						"role":          gin.H{"type": "string", "description": "Role to assign (e.g. coordinator, memory, worker). Defaults to worker"},
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
						"role":          gin.H{"type": "string", "description": "New role to assign"},
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
			{
				"name":        "list_roles",
				"description": "List all available persona roles",
				"endpoint":    "/mcp/list_roles",
				"parameters":  gin.H{"type": "object", "properties": gin.H{}},
			},
			{
				"name":        "get_persona_by_role",
				"description": "Get the persona assigned to a specific role (returns first match for singleton roles)",
				"endpoint":    "/mcp/get_persona_by_role",
				"parameters": gin.H{
					"type":       "object",
					"properties": gin.H{"role": gin.H{"type": "string", "description": "Role ID (e.g. coordinator, memory, worker)"}},
					"required":   []string{"role"},
				},
			},
			{
				"name":        "assign_role",
				"description": "Assign a role to a persona (enforces max_count constraints)",
				"endpoint":    "/mcp/assign_role",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"alias": gin.H{"type": "string", "description": "Persona alias"},
						"role":  gin.H{"type": "string", "description": "Role ID to assign"},
					},
					"required": []string{"alias", "role"},
				},
			},
	}
}

// GetTools handles GET /tools — returns MCP tool definitions.
func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// --- MCP persona handlers ---

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
	requestedRole := req.Role
	if requestedRole == "" {
		requestedRole = "worker"
	}
	// Auto-fill system_prompt from role default if not provided.
	if req.SystemPrompt == "" {
		if rolePrompt := h.db.GetRolePrompt(requestedRole); rolePrompt != "" {
			req.SystemPrompt = rolePrompt
		} else {
			req.SystemPrompt = h.defaultPromptForAlias(req.Alias)
		}
	}
	req.Role = "worker" // Create with worker first, then assign if needed.
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	if requestedRole != "worker" {
		if err := h.db.AssignRole(req.Alias, requestedRole); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	p, _ := h.db.Get(req.Alias)
	c.JSON(http.StatusCreated, p)
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

	// Handle role change via AssignRole.
	if roleVal, ok := body["role"]; ok {
		if roleStr, ok := roleVal.(string); ok && roleStr != "" {
			if err := h.db.AssignRole(alias, roleStr); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
	}

	updates := make(map[string]interface{})
	for _, key := range []string{"system_prompt", "model", "backend_alias"} {
		if v, ok := body[key]; ok {
			updates[key] = v
		}
	}
	if len(updates) > 0 {
		if err := h.db.Update(alias, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
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

// --- MCP role handlers ---

// MCPListRoles handles POST /mcp/list_roles.
func (h *Handler) MCPListRoles(c *gin.Context) {
	roles, err := h.db.ListRoles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

// MCPGetPersonaByRole handles POST /mcp/get_persona_by_role.
func (h *Handler) MCPGetPersonaByRole(c *gin.Context) {
	var req struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role is required"})
		return
	}
	p, err := h.db.GetPersonaByRole(req.Role)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no persona found with role: " + req.Role})
		return
	}
	c.JSON(http.StatusOK, p)
}

// MCPAssignRole handles POST /mcp/assign_role.
func (h *Handler) MCPAssignRole(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Alias == "" || req.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias and role are required"})
		return
	}
	if err := h.db.AssignRole(req.Alias, req.Role); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(req.Alias)
	c.JSON(http.StatusOK, p)
}
