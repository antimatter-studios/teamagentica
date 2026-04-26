package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/workspace-manager/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/workspace-manager/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	var h *handlers.Handler

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ChatCommands: []pluginsdk.ChatCommand{
			{
				Name: "list", Namespace: "workspace",
				Description: "List all workspaces with their current status",
				Endpoint:    "/chat-command/workspace/list",
			},
			{
				Name: "create", Namespace: "workspace",
				Description: "Create a new workspace",
				Endpoint:    "/chat-command/workspace/create",
				Params: []pluginsdk.ChatCommandParam{
					{Name: "name", Description: "Display name for the workspace", Type: "string", Required: true},
					{Name: "environment", Description: "Environment type (e.g. vscode, claude) — defaults to first available", Type: "string"},
				},
			},
			{
				Name: "rename", Namespace: "workspace",
				Description: "Rename an existing workspace",
				Endpoint:    "/chat-command/workspace/rename",
				Params: []pluginsdk.ChatCommandParam{
					{Name: "workspace", Description: "Current workspace name", Type: "string", Required: true},
					{Name: "name", Description: "New display name", Type: "string", Required: true},
				},
			},
		},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
		ToolsFunc: func() interface{} {
			return h.ToolDefs()
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	if v := pluginConfig["WORKSPACE_MANAGER_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	// Base domain for constructing workspace URLs.
	baseDomain := pluginConfig["TEAMAGENTICA_BASE_DOMAIN"]

	db, err := storage.Open("/data")
	if err != nil {
		log.Fatalf("failed to open workspace database: %v", err)
	}

	router := gin.Default()
	h = handlers.NewHandler(baseDomain, debug, db)
	h.SetSDK(sdkClient)

	// Listen for workspace environment registrations (push-based).
	sdkClient.Events().On("workspace:environment:register", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		h.HandleEnvironmentRegister(event.Detail)
	}))

	sdkClient.Events().Publish("workspace:manager:ready", `{"manager_plugin_id":"workspace-manager"}`)
	log.Println("emitted workspace:manager:ready")

	// Reconcile sidecar aliases once the alias registry is available —
	// ensures aliases exist for workspaces with attached agents after kernel restarts.
	sdkClient.OnPluginAvailable("alias:manage", func(_ pluginsdk.PluginInfo) {
		h.ReconcileSidecars(context.Background())
	})

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	router.GET("/health", h.Health)

	// Environment discovery — what workspace types are available.
	router.GET("/environments", h.ListEnvironments)

	// Workspace lifecycle — create, list, get, delete.
	router.GET("/workspaces", h.ListWorkspaces)
	router.POST("/workspaces", h.CreateWorkspace)
	router.GET("/workspaces/:id", h.GetWorkspace)
	router.PATCH("/workspaces/:id", h.RenameWorkspace)
	router.DELETE("/workspaces/:id", h.DeleteWorkspace)
	router.POST("/workspaces/:id/start", h.StartWorkspace)
	router.POST("/workspaces/:id/stop", h.StopWorkspace)
	router.POST("/workspaces/:id/restart", h.RestartWorkspace)
	router.GET("/workspaces/:id/options", h.GetWorkspaceOptions)
	router.PUT("/workspaces/:id/options", h.UpdateWorkspaceOptions)

	// Git persistence.
	router.POST("/workspaces/:id/persist", h.PersistWorkspace)

	// Generic chat command handlers (used by infra-chat-command-server).
	router.POST("/chat-command/workspace/list", h.ChatCommandWorkspaceList)
	router.POST("/chat-command/workspace/create", h.ChatCommandWorkspaceCreate)
	router.POST("/chat-command/workspace/rename", h.ChatCommandWorkspaceRename)

	// Tool interface for AI agents.
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/list_environments", h.ToolListEnvironments)
	router.POST("/mcp/create_workspace", h.ToolCreateWorkspace)
	router.POST("/mcp/list_workspaces", h.ToolListWorkspaces)
	router.POST("/mcp/start_workspace", h.ToolStartWorkspace)
	router.POST("/mcp/stop_workspace", h.ToolStopWorkspace)
	router.POST("/mcp/delete_workspace", h.ToolDeleteWorkspace)
	router.POST("/mcp/rename_workspace", h.ToolRenameWorkspace)
	router.POST("/mcp/build_plugin", h.ToolBuildPlugin)

	sdkClient.ListenAndServe(port, router)
}
