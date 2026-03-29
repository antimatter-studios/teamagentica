package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/storage"
)

// Handler serves persona API endpoints.
type Handler struct {
	db  *storage.DB
	sdk *pluginsdk.Client
}

// New creates a new Handler.
func New(db *storage.DB) *Handler {
	return &Handler{db: db}
}

// SetSDK attaches the SDK client for emitting events.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// BroadcastReady emits a persona:update event on startup so subscribers
// (e.g. the relay) that started before this plugin can load personas.
func (h *Handler) BroadcastReady() {
	h.broadcastPersonaChange("ready", "")
}

// broadcastPersonaChange emits a persona:update event so subscribers
// (e.g. the relay) can reactively refresh their persona cache.
func (h *Handler) broadcastPersonaChange(action, alias string) {
	if h.sdk == nil {
		return
	}
	events.PublishPersonaUpdate(h.sdk, action, alias)
}

// DB returns the underlying database (may be nil before init).
func (h *Handler) DB() *storage.DB { return h.db }

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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", alias)})
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
	if storage.Sanitize(req.Alias) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	// Validate role exists before creating persona to avoid orphaned rows.
	if req.Role != "" {
		if _, err := h.db.GetRole(req.Role); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("role %q not found", req.Role)})
			return
		}
	}
	// Auto-fill system_prompt from role if not provided.
	if req.SystemPrompt == "" && req.Role != "" {
		if rolePrompt := h.db.GetRolePrompt(req.Role); rolePrompt != "" {
			req.SystemPrompt = rolePrompt
		}
	}
	wantDefault := req.IsDefault != nil && *req.IsDefault
	req.IsDefault = nil // don't pass through GORM create; use SetDefault for atomicity
	requestedRole := req.Role
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	// Persona row exists from this point — always broadcast so caches update
	// even if subsequent steps (role/default) fail.
	defer h.broadcastPersonaChange("created", req.Alias)

	// Use AssignRole for singleton enforcement.
	if requestedRole != "" {
		if err := h.db.AssignRole(req.Alias, requestedRole); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if wantDefault {
		if err := h.db.SetDefault(req.Alias); err != nil {
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", alias)})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Persona exists — always broadcast so caches update even on partial failure.
	changed := false
	defer func() {
		if changed {
			h.broadcastPersonaChange("updated", alias)
		}
	}()

	// Handle role change via AssignRole.
	if roleVal, ok := body["role"]; ok {
		if roleStr, ok := roleVal.(string); ok {
			if err := h.db.AssignRole(alias, roleStr); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			changed = true
		}
	}

	// Handle is_default toggle.
	if v, ok := body["is_default"]; ok {
		if b, ok := v.(bool); ok {
			if b {
				if err := h.db.SetDefault(alias); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			} else {
				if err := h.db.ClearDefault(alias); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			}
			changed = true
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
		changed = true
	}

	// Handle rename (must be last — changes the primary key).
	if newAlias, ok := body["alias"]; ok {
		if s, ok := newAlias.(string); ok && s != "" && s != alias {
			if err := h.db.Rename(alias, s); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "rename failed: " + err.Error()})
				return
			}
			alias = storage.Sanitize(s) // track the sanitized name for broadcast/response
			changed = true
		}
	}

	p, _ := h.db.Get(alias)
	c.JSON(http.StatusOK, p)
}

// GetDefaultPersona handles GET /personas/default.
func (h *Handler) GetDefaultPersona(c *gin.Context) {
	p, err := h.db.GetDefault()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no default persona set"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// SetDefaultPersona handles POST /personas/:alias/set-default.
func (h *Handler) SetDefaultPersona(c *gin.Context) {
	alias := c.Param("alias")
	if _, err := h.db.Get(alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", alias)})
		return
	}
	if err := h.db.SetDefault(alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(alias)
	h.broadcastPersonaChange("updated", alias)
	c.JSON(http.StatusOK, p)
}

// DeletePersona handles DELETE /personas/:alias.
func (h *Handler) DeletePersona(c *gin.Context) {
	alias := c.Param("alias")
	if err := h.db.Delete(alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastPersonaChange("deleted", alias)
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("role %q not found", id)})
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
	if storage.Sanitize(req.ID) == "" {
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("role %q not found", id)})
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
					"role":          gin.H{"type": "string", "description": "Role to assign"},
					"is_default":    gin.H{"type": "boolean", "description": "Set as the default persona for unaddressed messages"},
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
					"is_default":    gin.H{"type": "boolean", "description": "Set as the default persona for unaddressed messages"},
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
				"properties": gin.H{"role": gin.H{"type": "string", "description": "Role ID"}},
				"required":   []string{"role"},
			},
		},
		{
			"name":        "get_default_persona",
			"description": "Get the persona currently set as the default for unaddressed messages",
			"endpoint":    "/mcp/get_default_persona",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		{
			"name":        "set_default_persona",
			"description": "Set a persona as the default for unaddressed messages (clears previous default)",
			"endpoint":    "/mcp/set_default_persona",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"alias": gin.H{"type": "string", "description": "Persona alias to set as default"}},
				"required":   []string{"alias"},
			},
		},
		{
			"name":        "assign_role",
			"description": "Assign a role to a persona (enforces singleton constraint)",
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", req.Alias)})
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
	if storage.Sanitize(req.Alias) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	// Validate role exists before creating persona to avoid orphaned rows.
	if req.Role != "" {
		if _, err := h.db.GetRole(req.Role); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("role %q not found", req.Role)})
			return
		}
	}
	// Auto-fill system_prompt from role if not provided.
	wantDefault := req.IsDefault != nil && *req.IsDefault
	req.IsDefault = nil
	requestedRole := req.Role
	if req.SystemPrompt == "" && requestedRole != "" {
		if rolePrompt := h.db.GetRolePrompt(requestedRole); rolePrompt != "" {
			req.SystemPrompt = rolePrompt
		}
	}
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "persona already exists"})
		return
	}
	// Persona row exists — always broadcast so caches update even if role/default fail.
	defer h.broadcastPersonaChange("created", req.Alias)

	if requestedRole != "" {
		if err := h.db.AssignRole(req.Alias, requestedRole); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if wantDefault {
		if err := h.db.SetDefault(req.Alias); err != nil {
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", alias)})
		return
	}

	// Persona exists — always broadcast so caches update even on partial failure.
	changed := false
	defer func() {
		if changed {
			h.broadcastPersonaChange("updated", alias)
		}
	}()

	// Handle role change via AssignRole.
	if roleVal, ok := body["role"]; ok {
		if roleStr, ok := roleVal.(string); ok {
			if err := h.db.AssignRole(alias, roleStr); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			changed = true
		}
	}

	// Handle is_default toggle.
	if v, ok := body["is_default"]; ok {
		if b, ok := v.(bool); ok {
			if b {
				if err := h.db.SetDefault(alias); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			} else {
				if err := h.db.ClearDefault(alias); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			}
			changed = true
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
		changed = true
	}

	// Handle rename via new_alias.
	if newAlias, ok := body["new_alias"]; ok {
		if s, ok := newAlias.(string); ok && s != "" && s != alias {
			if err := h.db.Rename(alias, s); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "rename failed: " + err.Error()})
				return
			}
			alias = storage.Sanitize(s) // track the sanitized name for broadcast/response
			changed = true
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
	h.broadcastPersonaChange("deleted", req.Alias)
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

// MCPGetDefaultPersona handles POST /mcp/get_default_persona.
func (h *Handler) MCPGetDefaultPersona(c *gin.Context) {
	p, err := h.db.GetDefault()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no default persona set"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// MCPSetDefaultPersona handles POST /mcp/set_default_persona.
func (h *Handler) MCPSetDefaultPersona(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if _, err := h.db.Get(req.Alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("persona %q not found", req.Alias)})
		return
	}
	if err := h.db.SetDefault(req.Alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(req.Alias)
	h.broadcastPersonaChange("updated", req.Alias)
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
	h.broadcastPersonaChange("updated", req.Alias)
	c.JSON(http.StatusOK, p)
}
