package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// MCPToolsHandler exposes selected kernel capabilities as MCP tools.
// It thin-wraps existing PluginHandler / MarketplaceHandler endpoints,
// adapting JSON bodies into the URL params those handlers expect.
type MCPToolsHandler struct {
	plugin      *PluginHandler
	marketplace *MarketplaceHandler
}

func NewMCPToolsHandler(plugin *PluginHandler, marketplace *MarketplaceHandler) *MCPToolsHandler {
	return &MCPToolsHandler{plugin: plugin, marketplace: marketplace}
}

// ToolDefs returns the MCP tool descriptors registered with infra-mcp-server
// under plugin_id="kernel". Endpoints are dispatched by the kernel's mTLS
// listener under /mcp/kernel/*.
func (h *MCPToolsHandler) ToolDefs() interface{} {
	emptyObject := gin.H{"type": "object", "properties": gin.H{}}
	pluginIDOnly := gin.H{
		"type": "object",
		"properties": gin.H{
			"plugin_id": gin.H{"type": "string", "description": "Plugin ID"},
		},
		"required": []string{"plugin_id"},
	}

	return []gin.H{
		{
			"name":        "list_plugins",
			"description": "List every plugin known to the kernel — running, stopped, disabled. Returns id, name, version, status, capabilities.",
			"endpoint":    "/mcp/kernel/list_plugins",
			"parameters":  emptyObject,
		},
		{
			"name":        "get_plugin",
			"description": "Get full record for a single plugin (config schema, image, capabilities, host, port, candidate state).",
			"endpoint":    "/mcp/kernel/get_plugin",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "get_plugin_status",
			"description": "Lightweight status check for a plugin: status, last_seen, enabled.",
			"endpoint":    "/mcp/kernel/get_plugin_status",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "get_plugin_logs",
			"description": "Fetch recent stdout/stderr from a plugin's container.",
			"endpoint":    "/mcp/kernel/get_plugin_logs",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"plugin_id": gin.H{"type": "string", "description": "Plugin ID"},
					"tail":      gin.H{"type": "string", "description": "Number of lines to return (default 200)"},
				},
				"required": []string{"plugin_id"},
			},
		},
		{
			"name":        "get_plugin_schema",
			"description": "Fetch the plugin's full /schema document — describes its config, tools, events, and any custom sections.",
			"endpoint":    "/mcp/kernel/get_plugin_schema",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "get_kernel_logs",
			"description": "Recent kernel log output. Use to inspect kernel-level activity: orchestration, lifecycle events, registration.",
			"endpoint":    "/mcp/kernel/get_kernel_logs",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"tail": gin.H{"type": "string", "description": "Number of lines to return (default 200)"},
				},
			},
		},
		{
			"name":        "list_managed_containers",
			"description": "List all managed containers across plugins (workspaces, sidecars, etc.) with their status.",
			"endpoint":    "/mcp/kernel/list_managed_containers",
			"parameters":  emptyObject,
		},
		{
			"name":        "enable_plugin",
			"description": "Enable a disabled plugin — starts its container and brings it into the running set. Cascades to dependencies.",
			"endpoint":    "/mcp/kernel/enable_plugin",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "disable_plugin",
			"description": "Disable a plugin — stops the container and marks it disabled so it will not be started by the orchestrator.",
			"endpoint":    "/mcp/kernel/disable_plugin",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "restart_plugin",
			"description": "Restart a running plugin's container in place.",
			"endpoint":    "/mcp/kernel/restart_plugin",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "deploy_candidate",
			"description": "Deploy a candidate container alongside the primary. Used for blue/green-style plugin upgrades. Optionally provide an image override.",
			"endpoint":    "/mcp/kernel/deploy_candidate",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"plugin_id": gin.H{"type": "string", "description": "Plugin ID"},
					"image":     gin.H{"type": "string", "description": "Docker image (optional, defaults to plugin's configured candidate image)"},
				},
				"required": []string{"plugin_id"},
			},
		},
		{
			"name":        "promote_candidate",
			"description": "Promote a candidate to become the primary. Stops the old primary.",
			"endpoint":    "/mcp/kernel/promote_candidate",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "rollback_candidate",
			"description": "Stop a candidate and revert traffic to the primary.",
			"endpoint":    "/mcp/kernel/rollback_candidate",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "install_plugin",
			"description": "Install a plugin from a marketplace provider. If provider_id is omitted, the first enabled provider is used.",
			"endpoint":    "/mcp/kernel/install_plugin",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"plugin_id":   gin.H{"type": "string", "description": "Catalog plugin ID to install"},
					"provider_id": gin.H{"type": "integer", "description": "Marketplace provider numeric ID (optional)"},
				},
				"required": []string{"plugin_id"},
			},
		},
		{
			"name":        "upgrade_plugin",
			"description": "Upgrade an installed plugin to the latest available version from its marketplace provider.",
			"endpoint":    "/mcp/kernel/upgrade_plugin",
			"parameters":  pluginIDOnly,
		},
		{
			"name":        "uninstall_plugin",
			"description": "Permanently uninstall a plugin (system plugins cannot be uninstalled). Stops the container and removes the record.",
			"endpoint":    "/mcp/kernel/uninstall_plugin",
			"parameters":  pluginIDOnly,
		},
	}
}

// readBodyJSON reads & decodes the request body, then resets it so the wrapped
// handler can re-read via ShouldBindJSON.
func readBodyJSON(c *gin.Context, dst interface{}) error {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// withPluginID extracts plugin_id from JSON body, binds it as the :id route param,
// and restores the body so the underlying handler can read it again.
func withPluginID(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PluginID string `json:"plugin_id"`
		}
		if err := readBodyJSON(c, &req); err != nil || req.PluginID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "plugin_id is required"})
			return
		}
		c.Params = append(c.Params[:0], gin.Param{Key: "id", Value: req.PluginID})
		next(c)
	}
}

// withPluginIDAndTail also surfaces a "tail" body field as a query param.
func withPluginIDAndTail(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PluginID string `json:"plugin_id"`
			Tail     string `json:"tail"`
		}
		if err := readBodyJSON(c, &req); err != nil || req.PluginID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "plugin_id is required"})
			return
		}
		c.Params = append(c.Params[:0], gin.Param{Key: "id", Value: req.PluginID})
		if req.Tail != "" {
			q := c.Request.URL.Query()
			q.Set("tail", req.Tail)
			c.Request.URL.RawQuery = q.Encode()
		}
		next(c)
	}
}

// withTailOnly surfaces a body "tail" field as a query param for kernel log tools.
func withTailOnly(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Tail string `json:"tail"`
		}
		_ = readBodyJSON(c, &req)
		if req.Tail != "" {
			q := c.Request.URL.Query()
			q.Set("tail", req.Tail)
			c.Request.URL.RawQuery = q.Encode()
		}
		next(c)
	}
}

// Register wires every kernel MCP tool route under the given group.
// The group should already require mTLS auth (PluginTokenAuth) since calls
// arrive from infra-mcp-server over the kernel's plugin port.
func (h *MCPToolsHandler) Register(rg *gin.RouterGroup) {
	rg.POST("/list_plugins", h.plugin.ListPlugins)
	rg.POST("/get_plugin", withPluginID(h.plugin.GetPlugin))
	rg.POST("/get_plugin_status", withPluginID(h.plugin.GetPluginStatus))
	rg.POST("/get_plugin_logs", withPluginIDAndTail(h.plugin.GetPluginLogs))
	rg.POST("/get_plugin_schema", withPluginID(h.plugin.GetPluginSchema))
	rg.POST("/get_kernel_logs", withTailOnly(h.plugin.GetKernelLogs))
	rg.POST("/list_managed_containers", h.plugin.ListAllManagedContainers)

	rg.POST("/enable_plugin", withPluginID(h.plugin.EnablePlugin))
	rg.POST("/disable_plugin", withPluginID(h.plugin.DisablePlugin))
	rg.POST("/restart_plugin", withPluginID(h.plugin.RestartPlugin))
	rg.POST("/deploy_candidate", withPluginID(h.plugin.DeployCandidate))
	rg.POST("/promote_candidate", withPluginID(h.plugin.PromoteCandidate))
	rg.POST("/rollback_candidate", withPluginID(h.plugin.RollbackCandidate))
	rg.POST("/uninstall_plugin", withPluginID(h.plugin.UninstallPlugin))

	if h.marketplace != nil {
		rg.POST("/install_plugin", h.marketplace.InstallPlugin)
		rg.POST("/upgrade_plugin", h.marketplace.UpgradePlugin)
	}
}

// PushToMCPServer registers the kernel's tools with infra-mcp-server.
// Returns nil immediately on success or if the server isn't running yet.
func (h *MCPToolsHandler) PushToMCPServer(ctx context.Context) error {
	var srv models.Plugin
	if err := database.Get().Select("id", "host", "http_port", "status").
		Where("id = ? AND status = ? AND host != ''", "infra-mcp-server", "running").
		First(&srv).Error; err != nil {
		return fmt.Errorf("infra-mcp-server not running")
	}

	body, err := json.Marshal(map[string]interface{}{
		"plugin_id": "kernel",
		"tools":     h.ToolDefs(),
	})
	if err != nil {
		return err
	}

	scheme := "http"
	if h.plugin.clientTLS != nil {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s:%d/tools/register", scheme, srv.Host, srv.HTTPPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second, Transport: h.plugin.transport}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mcp-server register returned %d: %s", resp.StatusCode, string(b))
	}
	log.Printf("kernel: pushed tools to infra-mcp-server")
	return nil
}

// WaitAndPush polls until infra-mcp-server is running, then pushes tools.
// Stops on context cancel. Logs (but does not fatal) on push failure.
func (h *MCPToolsHandler) WaitAndPush(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		if err := h.PushToMCPServer(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
