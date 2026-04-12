package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-registry/internal/storage"
)

// Handler serves agent registry API endpoints.
type Handler struct {
	db            *storage.DB
	sdk           *pluginsdk.Client
	defaultPrompt string
}

// New creates a new Handler.
func New(db *storage.DB, defaultPrompt string) *Handler {
	return &Handler{db: db, defaultPrompt: defaultPrompt}
}

// SetSDK attaches the SDK client for emitting events.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// BroadcastReady emits agent:update and agent:ready events on startup so
// subscribers (relay, messaging plugins) that started before this plugin can
// load agents and populate their alias maps.
func (h *Handler) BroadcastReady() {
	if h.sdk == nil {
		return
	}
	h.sdk.PublishEvent("agent:update", `{"action":"ready","alias":{}}`)
	h.sdk.PublishEvent("agent:ready", "")
}

// broadcastChange emits an agent:update event with alias-compatible
// payload shape so that consumers (relay, messaging plugins) can patch their
// alias maps and refresh agent caches from a single event.
func (h *Handler) broadcastChange(action string, p *storage.Persona) {
	if h.sdk == nil {
		return
	}
	aliasPayload := map[string]interface{}{
		"name":   p.Alias,
		"type":   p.Type,
		"plugin": p.Plugin,
		"model":  p.Model,
	}
	detail, _ := json.Marshal(map[string]interface{}{
		"action": action,
		"alias":  aliasPayload,
	})
	h.sdk.PublishEvent("agent:update", string(detail))
}

// DB returns the underlying database (may be nil before init).
func (h *Handler) DB() *storage.DB { return h.db }

// Health handles GET /health.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- Agent REST endpoints ---

// ListAgents handles GET /agents — returns only entries with system_prompt or is_default.
func (h *Handler) ListAgents(c *gin.Context) {
	agents, err := h.db.ListAgents()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// GetAgent handles GET /agents/:alias.
func (h *Handler) GetAgent(c *gin.Context) {
	alias := c.Param("alias")
	p, err := h.db.Get(alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", alias)})
		return
	}
	c.JSON(http.StatusOK, p)
}

// CreateAgent handles POST /agents.
func (h *Handler) CreateAgent(c *gin.Context) {
	var req storage.Persona
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if storage.Sanitize(req.Alias) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}

	wantDefault := req.IsDefault != nil && *req.IsDefault
	req.IsDefault = nil
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "agent already exists"})
		return
	}
	defer func() {
		if p, err := h.db.Get(req.Alias); err == nil {
			h.broadcastChange("created", p)
		}
	}()

	if wantDefault {
		if err := h.db.SetDefault(req.Alias); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	p, _ := h.db.Get(req.Alias)
	c.JSON(http.StatusCreated, p)
}

// UpdateAgent handles PUT /agents/:alias.
func (h *Handler) UpdateAgent(c *gin.Context) {
	alias := c.Param("alias")

	if _, err := h.db.Get(alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", alias)})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	changed := false
	defer func() {
		if changed {
			if p, err := h.db.Get(alias); err == nil {
				h.broadcastChange("updated", p)
			}
		}
	}()

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
	for _, key := range []string{"system_prompt", "model", "type", "plugin"} {
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

	if newAlias, ok := body["alias"]; ok {
		if s, ok := newAlias.(string); ok && s != "" && s != alias {
			if err := h.db.Rename(alias, s); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "rename failed: " + err.Error()})
				return
			}
			alias = storage.Sanitize(s)
			changed = true
		}
	}

	p, _ := h.db.Get(alias)
	c.JSON(http.StatusOK, p)
}

// GetDefaultAgent handles GET /agents/default.
func (h *Handler) GetDefaultAgent(c *gin.Context) {
	p, err := h.db.GetDefault()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no default agent set"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// SetDefaultAgent handles POST /agents/:alias/set-default.
func (h *Handler) SetDefaultAgent(c *gin.Context) {
	alias := c.Param("alias")
	if _, err := h.db.Get(alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", alias)})
		return
	}
	if err := h.db.SetDefault(alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(alias)
	h.broadcastChange("updated", p)
	c.JSON(http.StatusOK, p)
}

// ListAgentsByType handles GET /agents/by-type/:type.
func (h *Handler) ListAgentsByType(c *gin.Context) {
	t := c.Param("type")
	agents, err := h.db.ListByType(t)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// DeleteAgent handles DELETE /agents/:alias.
func (h *Handler) DeleteAgent(c *gin.Context) {
	alias := c.Param("alias")
	p, err := h.db.Get(alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", alias)})
		return
	}
	if err := h.db.Delete(alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastChange("deleted", p)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// --- Alias REST endpoints ---

// ListAliases handles GET /aliases — returns all entries (unfiltered) for routing consumers,
// or bare aliases only when ?exclude_agents=true.
func (h *Handler) ListAliases(c *gin.Context) {
	aliasType := c.Query("type")
	excludeAgents := c.Query("exclude_agents") == "true"
	var entries []storage.Persona
	var err error
	if aliasType != "" {
		entries, err = h.db.ListByType(aliasType)
	} else if excludeAgents {
		entries, err = h.db.ListAliases()
	} else {
		entries, err = h.db.List()
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type aliasResp struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		Plugin       string `json:"plugin"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt,omitempty"`
	}
	aliases := make([]aliasResp, 0, len(entries))
	for _, p := range entries {
		aliases = append(aliases, aliasResp{
			Name:         p.Alias,
			Type:         p.Type,
			Plugin:       p.Plugin,
			Model:        p.Model,
			SystemPrompt: p.SystemPrompt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"aliases": aliases})
}

// CreateAlias handles POST /aliases.
func (h *Handler) CreateAlias(c *gin.Context) {
	var req struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		Plugin       string `json:"plugin"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if storage.Sanitize(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.Plugin == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plugin is required"})
		return
	}
	p := &storage.Persona{
		Alias:        req.Name,
		Type:         req.Type,
		Plugin:       req.Plugin,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	}
	if err := h.db.Create(p); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "alias already exists"})
		return
	}
	created, _ := h.db.Get(p.Alias)
	h.broadcastChange("created", created)
	c.JSON(http.StatusCreated, created)
}

// UpdateAlias handles PUT /aliases/:name.
func (h *Handler) UpdateAlias(c *gin.Context) {
	name := c.Param("name")
	if _, err := h.db.Get(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("alias %q not found", name)})
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := make(map[string]interface{})
	for _, key := range []string{"type", "plugin", "model", "system_prompt"} {
		if v, ok := body[key]; ok {
			updates[key] = v
		}
	}
	if len(updates) > 0 {
		if err := h.db.Update(name, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	p, _ := h.db.Get(name)
	h.broadcastChange("updated", p)
	c.JSON(http.StatusOK, p)
}

// DeleteAlias handles DELETE /aliases/:name.
func (h *Handler) DeleteAlias(c *gin.Context) {
	name := c.Param("name")
	p, err := h.db.Get(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("alias %q not found", name)})
		return
	}
	if err := h.db.Delete(name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastChange("deleted", p)
	c.Status(http.StatusNoContent)
}

// GetAlias handles GET /alias/:name.
func (h *Handler) GetAlias(c *gin.Context) {
	name := c.Param("name")
	p, err := h.db.Get(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("alias %q not found", name)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"name":          p.Alias,
		"type":          p.Type,
		"plugin":        p.Plugin,
		"model":         p.Model,
		"system_prompt": p.SystemPrompt,
	})
}

// --- MCP tool definitions ---

// ToolDefs returns the MCP tool definitions.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "list_agents",
			"description": "List all configured agents and aliases",
			"endpoint":    "/mcp/list_agents",
			"parameters":  gin.H{"type": "object", "properties": gin.H{"type": gin.H{"type": "string", "description": "Filter by type: agent, tool_agent, tool"}}},
		},
		{
			"name":        "get_agent",
			"description": "Get a specific agent by alias",
			"endpoint":    "/mcp/get_agent",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"alias": gin.H{"type": "string", "description": "Agent alias"}},
				"required":   []string{"alias"},
			},
		},
		{
			"name":        "create_agent",
			"description": "Create a new agent",
			"endpoint":    "/mcp/create_agent",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"alias":         gin.H{"type": "string", "description": "Unique alias"},
					"type":          gin.H{"type": "string", "description": "Type: agent, tool_agent, or tool"},
					"plugin":        gin.H{"type": "string", "description": "Target plugin ID (e.g. agent-anthropic)"},
					"model":         gin.H{"type": "string", "description": "Model override (optional)"},
					"system_prompt": gin.H{"type": "string", "description": "System prompt (optional — falls back to default)"},
					"is_default":    gin.H{"type": "boolean", "description": "Set as default agent for unaddressed messages"},
				},
				"required": []string{"alias", "plugin"},
			},
		},
		{
			"name":        "update_agent",
			"description": "Update an existing agent",
			"endpoint":    "/mcp/update_agent",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"alias":         gin.H{"type": "string", "description": "Agent alias to update"},
					"type":          gin.H{"type": "string", "description": "New type"},
					"plugin":        gin.H{"type": "string", "description": "New target plugin ID"},
					"model":         gin.H{"type": "string", "description": "New model override"},
					"system_prompt": gin.H{"type": "string", "description": "New system prompt"},
					"is_default":    gin.H{"type": "boolean", "description": "Set as default agent"},
				},
				"required": []string{"alias"},
			},
		},
		{
			"name":        "delete_agent",
			"description": "Delete an agent",
			"endpoint":    "/mcp/delete_agent",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"alias": gin.H{"type": "string", "description": "Agent alias to delete"}},
				"required":   []string{"alias"},
			},
		},
		{
			"name":        "get_default_agent",
			"description": "Get the agent currently set as default for unaddressed messages",
			"endpoint":    "/mcp/get_default_agent",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		{
			"name":        "set_default_agent",
			"description": "Set an agent as default for unaddressed messages",
			"endpoint":    "/mcp/set_default_agent",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"alias": gin.H{"type": "string", "description": "Agent alias to set as default"}},
				"required":   []string{"alias"},
			},
		},
	}
}

// GetTools handles GET /mcp — returns MCP tool definitions.
func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// --- MCP agent handlers ---

// MCPListAgents handles POST /mcp/list_agents.
func (h *Handler) MCPListAgents(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	_ = c.ShouldBindJSON(&req)

	var agents []storage.Persona
	var err error
	if req.Type != "" {
		agents, err = h.db.ListByType(req.Type)
	} else {
		agents, err = h.db.List()
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// MCPGetAgent handles POST /mcp/get_agent.
func (h *Handler) MCPGetAgent(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	p, err := h.db.Get(req.Alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", req.Alias)})
		return
	}
	c.JSON(http.StatusOK, p)
}

// MCPCreateAgent handles POST /mcp/create_agent.
func (h *Handler) MCPCreateAgent(c *gin.Context) {
	var req storage.Persona
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if storage.Sanitize(req.Alias) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}

	wantDefault := req.IsDefault != nil && *req.IsDefault
	req.IsDefault = nil
	if err := h.db.Create(&req); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "agent already exists"})
		return
	}
	defer func() {
		if p, err := h.db.Get(req.Alias); err == nil {
			h.broadcastChange("created", p)
		}
	}()

	if wantDefault {
		if err := h.db.SetDefault(req.Alias); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	p, _ := h.db.Get(req.Alias)
	c.JSON(http.StatusCreated, p)
}

// MCPUpdateAgent handles POST /mcp/update_agent.
func (h *Handler) MCPUpdateAgent(c *gin.Context) {
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", alias)})
		return
	}

	changed := false
	defer func() {
		if changed {
			if p, err := h.db.Get(alias); err == nil {
				h.broadcastChange("updated", p)
			}
		}
	}()

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
	for _, key := range []string{"system_prompt", "model", "type", "plugin"} {
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

	if newAlias, ok := body["new_alias"]; ok {
		if s, ok := newAlias.(string); ok && s != "" && s != alias {
			if err := h.db.Rename(alias, s); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "rename failed: " + err.Error()})
				return
			}
			alias = storage.Sanitize(s)
			changed = true
		}
	}

	p, _ := h.db.Get(alias)
	c.JSON(http.StatusOK, p)
}

// MCPDeleteAgent handles POST /mcp/delete_agent.
func (h *Handler) MCPDeleteAgent(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	p, err := h.db.Get(req.Alias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", req.Alias)})
		return
	}
	if err := h.db.Delete(req.Alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastChange("deleted", p)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// MCPGetDefaultAgent handles POST /mcp/get_default_agent.
func (h *Handler) MCPGetDefaultAgent(c *gin.Context) {
	p, err := h.db.GetDefault()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no default agent set"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// MCPSetDefaultAgent handles POST /mcp/set_default_agent.
func (h *Handler) MCPSetDefaultAgent(c *gin.Context) {
	var req struct {
		Alias string `json:"alias"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias is required"})
		return
	}
	if _, err := h.db.Get(req.Alias); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("agent %q not found", req.Alias)})
		return
	}
	if err := h.db.SetDefault(req.Alias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p, _ := h.db.Get(req.Alias)
	h.broadcastChange("updated", p)
	c.JSON(http.StatusOK, p)
}
