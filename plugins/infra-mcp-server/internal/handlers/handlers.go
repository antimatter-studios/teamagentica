package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
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
	tools := mcpserver.DiscoverTools(h.sdk, h.mcpSrv.Aliases())
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

func (h *Handler) Tools(c *gin.Context) {
	tools := mcpserver.DiscoverTools(h.sdk, h.mcpSrv.Aliases())
	c.JSON(http.StatusOK, gin.H{
		"tools": tools,
	})
}
