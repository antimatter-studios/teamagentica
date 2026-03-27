package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-alias-registry/internal/storage"
)

// Handler serves REST + MCP endpoints for alias management.
type Handler struct {
	db  *storage.DB
	sdk *pluginsdk.Client
}

// New creates a Handler backed by the given DB and SDK client.
func New(db *storage.DB, sdk *pluginsdk.Client) *Handler {
	return &Handler{db: db, sdk: sdk}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// broadcastAliasChange emits an alias-registry:update event for a single alias change.
func (h *Handler) broadcastAliasChange(action string, a *storage.Alias) {
	detail, _ := json.Marshal(map[string]interface{}{
		"action": action,
		"alias":  a,
	})
	h.sdk.ReportEvent("alias-registry:update", string(detail))
}

// ── Alias REST API ───────────────────────────────────────────────────────────

// GetAlias handles GET /alias/:name — used by the relay for fast lookup.
func (h *Handler) GetAlias(c *gin.Context) {
	a, err := h.db.Get(c.Param("name"))
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "alias not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, a)
}

// ListAliases handles GET /aliases — returns all aliases.
func (h *Handler) ListAliases(c *gin.Context) {
	aliasType := c.Query("type")
	var aliases []storage.Alias
	var err error
	if aliasType != "" {
		aliases, err = h.db.ListByType(aliasType)
	} else {
		aliases, err = h.db.List()
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"aliases": aliases})
}

// CreateAlias handles POST /aliases.
func (h *Handler) CreateAlias(c *gin.Context) {
	var req struct {
		Name         string `json:"name" binding:"required"`
		Type         string `json:"type" binding:"required"`
		Plugin       string `json:"plugin" binding:"required"`
		Provider     string `json:"provider"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	a := &storage.Alias{
		Name:         req.Name,
		Type:         req.Type,
		Plugin:       req.Plugin,
		Provider:     req.Provider,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	}
	if err := h.db.Create(a); errors.Is(err, storage.ErrAlreadyExists) {
		c.JSON(http.StatusConflict, gin.H{"error": "alias already exists"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastAliasChange("created", a)
	c.JSON(http.StatusCreated, a)
}

// UpdateAlias handles PUT /aliases/:name.
func (h *Handler) UpdateAlias(c *gin.Context) {
	name := c.Param("name")
	a, err := h.db.Get(name)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "alias not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Name         *string `json:"name"`
		Type         *string `json:"type"`
		Plugin       *string `json:"plugin"`
		Provider     *string `json:"provider"`
		Model        *string `json:"model"`
		SystemPrompt *string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type != nil {
		a.Type = *req.Type
	}
	if req.Plugin != nil {
		a.Plugin = *req.Plugin
	}
	if req.Provider != nil {
		a.Provider = *req.Provider
	}
	if req.Model != nil {
		a.Model = *req.Model
	}
	if req.SystemPrompt != nil {
		a.SystemPrompt = *req.SystemPrompt
	}

	if err := h.db.Update(a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Handle rename (must be last — changes the primary key).
	if req.Name != nil && *req.Name != "" && *req.Name != name {
		lowered := strings.ToLower(strings.TrimSpace(*req.Name))
		req.Name = &lowered
		if err := h.db.Rename(name, *req.Name); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "rename failed: " + err.Error()})
			return
		}
		a.Name = *req.Name
	}

	h.broadcastAliasChange("updated", a)
	c.JSON(http.StatusOK, a)
}

// DeleteAlias handles DELETE /aliases/:name.
func (h *Handler) DeleteAlias(c *gin.Context) {
	err := h.db.Delete(c.Param("name"))
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "alias not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastAliasChange("deleted", &storage.Alias{Name: c.Param("name")})
	c.Status(http.StatusNoContent)
}

// ── Backward-compatible persona endpoints (used by relay) ────────────────────

// GetPersona handles GET /persona/:alias — relay uses this for fast persona lookup.
func (h *Handler) GetPersona(c *gin.Context) {
	a, err := h.db.Get(c.Param("alias"))
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Return in persona-compatible shape for relay.
	c.JSON(http.StatusOK, gin.H{
		"alias":         a.Name,
		"system_prompt": a.SystemPrompt,
		"backend_alias": a.Provider,
		"model":         a.Model,
	})
}

// ── MCP tool discovery ───────────────────────────────────────────────────────

func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "list_aliases",
			"description": "List all registered aliases (agents, tool agents, tools)",
			"endpoint":    "/mcp/list_aliases",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		{
			"name":        "get_alias",
			"description": "Get a specific alias by name",
			"endpoint":    "/mcp/get_alias",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"name": gin.H{"type": "string", "description": "The alias name"}},
				"required":   []string{"name"},
			},
		},
		{
			"name":        "create_alias",
			"description": "Create a new alias (agent, tool_agent, or tool)",
			"endpoint":    "/mcp/create_alias",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":          gin.H{"type": "string", "description": "Unique alias name (e.g. coder, nanobanana, cloud)"},
					"type":          gin.H{"type": "string", "description": "Type: agent, tool_agent, or tool"},
					"plugin":        gin.H{"type": "string", "description": "Target plugin ID (e.g. agent-claude, tool-nanobanana, storage-sss3)"},
					"provider":      gin.H{"type": "string", "description": "For agents: provider plugin ID"},
					"model":         gin.H{"type": "string", "description": "For agents: model override"},
					"system_prompt": gin.H{"type": "string", "description": "For agents: system prompt"},
				},
				"required": []string{"name", "type", "plugin"},
			},
		},
		{
			"name":        "update_alias",
			"description": "Update an existing alias",
			"endpoint":    "/mcp/update_alias",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"name":          gin.H{"type": "string", "description": "The alias to update"},
					"type":          gin.H{"type": "string", "description": "New type"},
					"plugin":        gin.H{"type": "string", "description": "New target plugin"},
					"provider":      gin.H{"type": "string", "description": "New provider"},
					"model":         gin.H{"type": "string", "description": "New model"},
					"system_prompt": gin.H{"type": "string", "description": "New system prompt"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "delete_alias",
			"description": "Delete an alias",
			"endpoint":    "/mcp/delete_alias",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{"name": gin.H{"type": "string", "description": "The alias to delete"}},
				"required":   []string{"name"},
			},
		},
		{
			"name":        "migrate_from_kernel",
			"description": "Import all aliases from the kernel into the alias registry (skips existing)",
			"endpoint":    "/mcp/migrate_from_kernel",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
	}
}

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// ── MCP tool execution ───────────────────────────────────────────────────────

func (h *Handler) MCPListAliases(c *gin.Context) {
	aliases, err := h.db.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"aliases": aliases})
}

func (h *Handler) MCPGetAlias(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.db.Get(req.Name)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"error": "alias not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, a)
}

func (h *Handler) MCPCreateAlias(c *gin.Context) {
	var req struct {
		Name         string `json:"name" binding:"required"`
		Type         string `json:"type" binding:"required"`
		Plugin       string `json:"plugin" binding:"required"`
		Provider     string `json:"provider"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	a := &storage.Alias{
		Name:         req.Name,
		Type:         req.Type,
		Plugin:       req.Plugin,
		Provider:     req.Provider,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	}
	if err := h.db.Create(a); errors.Is(err, storage.ErrAlreadyExists) {
		c.JSON(http.StatusOK, gin.H{"error": "alias already exists"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastAliasChange("created", a)
	c.JSON(http.StatusOK, gin.H{"alias": a, "message": "alias created"})
}

func (h *Handler) MCPUpdateAlias(c *gin.Context) {
	var req struct {
		Name         string `json:"name" binding:"required"`
		Type         string `json:"type"`
		Plugin       string `json:"plugin"`
		Provider     string `json:"provider"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.db.Get(req.Name)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"error": "alias not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Type != "" {
		a.Type = req.Type
	}
	if req.Plugin != "" {
		a.Plugin = req.Plugin
	}
	a.Provider = req.Provider
	a.Model = req.Model
	a.SystemPrompt = req.SystemPrompt

	if err := h.db.Update(a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastAliasChange("updated", a)
	c.JSON(http.StatusOK, gin.H{"alias": a, "message": "alias updated"})
}

func (h *Handler) MCPDeleteAlias(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Delete(req.Name); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"error": "alias not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.broadcastAliasChange("deleted", &storage.Alias{Name: req.Name})
	c.JSON(http.StatusOK, gin.H{"message": "alias deleted"})
}

// ── Migration ─────────────────────────────────────────────────────────────────

// parseKernelAlias converts a kernel AliasInfo into a storage.Alias.
func parseKernelAlias(info alias.AliasInfo) *storage.Alias {
	target := strings.TrimSpace(info.Target)
	plugin := target
	model := ""
	if idx := strings.Index(target, ":"); idx > 0 {
		plugin = target[:idx]
		model = target[idx+1:]
	}

	aliasType := storage.TypeTool
	if strings.HasPrefix(plugin, "agent-") {
		aliasType = storage.TypeAgent
	} else if strings.HasPrefix(plugin, "tool-") {
		aliasType = storage.TypeToolAgent
	}

	a := &storage.Alias{
		Name:   strings.ToLower(strings.TrimSpace(info.Name)),
		Type:   aliasType,
		Plugin: plugin,
		Model:  model,
	}

	if aliasType == storage.TypeAgent {
		a.Provider = plugin
		a.SystemPrompt = fmt.Sprintf("You are @%s, a helpful AI assistant.", a.Name)
	}

	return a
}

// MigrateFromKernel handles POST /migrate-from-kernel — imports aliases from the kernel.
func (h *Handler) MigrateFromKernel(c *gin.Context) {
	infos, err := h.sdk.FetchAliases()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch kernel aliases: " + err.Error()})
		return
	}

	migrated, skipped := 0, 0
	for _, info := range infos {
		a := parseKernelAlias(info)
		if err := h.db.Create(a); errors.Is(err, storage.ErrAlreadyExists) {
			skipped++
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create alias " + a.Name + ": " + err.Error()})
			return
		} else {
			migrated++
			h.broadcastAliasChange("created", a)
		}
	}
	c.JSON(http.StatusOK, gin.H{"migrated": migrated, "skipped": skipped, "total": len(infos)})
}

// MCPMigrateFromKernel is the MCP tool version of MigrateFromKernel.
func (h *Handler) MCPMigrateFromKernel(c *gin.Context) {
	h.MigrateFromKernel(c)
}
