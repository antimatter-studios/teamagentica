package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	mcpserver "github.com/antimatter-studios/teamagentica/plugins/infra-mcp-server/internal/mcp"
)

type Handler struct {
	pluginID string
	debug    bool
	sdk      *pluginsdk.Client
	mcpSrv   *mcpserver.Server
}

func NewHandler(pluginID string, debug bool) *Handler {
	return &Handler{pluginID: pluginID, debug: debug}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
	h.mcpSrv = mcpserver.NewServer(sdk, h.debug)
}

// MCPServer returns the underlying MCP server for event wiring.
func (h *Handler) MCPServer() *mcpserver.Server {
	return h.mcpSrv
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"plugin": h.pluginID,
	})
}

func (h *Handler) Info(c *gin.Context) {
	tools := mcpserver.BuildToolList(h.mcpSrv.Aliases())
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.FullName
	}

	c.JSON(http.StatusOK, gin.H{
		"plugin":          h.pluginID,
		"protocol":        "mcp",
		"transport":       "streamable-http",
		"endpoint":        "/mcp",
		"discovered_tools": len(tools),
		"tools":           toolNames,
	})
}

// MCP handles all MCP protocol requests via mcp-go's StreamableHTTPServer.
// Supports POST (client requests), GET (SSE stream), DELETE (session cleanup).
func (h *Handler) MCP(c *gin.Context) {
	h.mcpSrv.HTTPServer().ServeHTTP(c.Writer, c.Request)
}

// RegisterTools accepts push-based tool registration from plugins.
// POST /tools/register with {plugin_id, tools: [{name, description, endpoint, parameters}]}
func (h *Handler) RegisterTools(c *gin.Context) {
	var req struct {
		PluginID string `json:"plugin_id" binding:"required"`
		Tools    []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Endpoint    string          `json:"endpoint"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tools := mcpserver.ToRawTools(req.PluginID, req.Tools)
	mcpserver.RegisterTools(req.PluginID, tools)

	// Refresh mcp-go registry.
	h.mcpSrv.RefreshTools()

	// Notify subscribers that the MCP tool list has changed.
	events.PublishMCPToolsChanged(h.sdk)

	log.Printf("mcp-server: %s registered %d tools via push", req.PluginID, len(req.Tools))
	c.JSON(http.StatusOK, gin.H{"registered": len(req.Tools)})
}
