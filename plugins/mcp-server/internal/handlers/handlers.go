package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/mcp-server/internal/config"
	mcpserver "github.com/antimatter-studios/teamagentica/plugins/mcp-server/internal/mcp"
)

type Handler struct {
	cfg    *config.Config
	sdk    *pluginsdk.Client
	mcpSrv *mcpserver.Server
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
	h.mcpSrv = mcpserver.NewServer(sdk, h.cfg.Debug)
}

// MCPServer returns the underlying MCP server for event wiring.
func (h *Handler) MCPServer() *mcpserver.Server {
	return h.mcpSrv
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"plugin": h.cfg.PluginID,
	})
}

func (h *Handler) Info(c *gin.Context) {
	tools := mcpserver.DiscoverTools(h.sdk, h.mcpSrv.Aliases())
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.FullName
	}

	c.JSON(http.StatusOK, gin.H{
		"plugin":          h.cfg.PluginID,
		"protocol":        "mcp",
		"transport":       "streamable-http",
		"endpoint":        "/mcp",
		"discovered_tools": len(tools),
		"tools":           toolNames,
	})
}
