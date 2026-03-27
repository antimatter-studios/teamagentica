package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-workspace-manager/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-workspace-manager/internal/storage"
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
		DiscordCommands: []pluginsdk.DiscordCommand{
			{
				Name:        "workspace",
				Description: "Manage workspaces",
				Subcommands: []pluginsdk.DiscordSubcommand{
					{
						Name:        "list",
						Description: "List all workspaces with their current status",
						Endpoint:    "/discord-command/workspace/list",
					},
					{
						Name:        "create",
						Description: "Create a new workspace",
						Endpoint:    "/discord-command/workspace/create",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "name", Description: "Display name for the workspace", Type: "string", Required: true},
							{Name: "environment", Description: "Environment type (e.g. vscode, claude) — defaults to first available", Type: "string"},
						},
					},
					{
						Name:        "rename",
						Description: "Rename an existing workspace",
						Endpoint:    "/discord-command/workspace/rename",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "workspace", Description: "Current workspace name", Type: "string", Required: true},
							{Name: "name", Description: "New display name", Type: "string", Required: true},
						},
					},
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

	// Data dir is /data (storage-disk's data, cross-mounted by the kernel).
	// Volumes live at /data/volumes/.
	workspaceDir := "/data"
	if err := os.MkdirAll(workspaceDir+"/volumes", 0755); err != nil {
		log.Printf("WARNING: failed to create workspace volumes dir: %v (some operations may fail)", err)
	}

	// Local SQLite database for workspace-manager-level metadata
	// (environment tracking, etc.) — kept separate from the kernel.
	db, err := storage.Open(workspaceDir)
	if err != nil {
		log.Fatalf("failed to open workspace database: %v", err)
	}

	router := gin.Default()
	h = handlers.NewHandler(workspaceDir, baseDomain, debug, db)
	h.SetSDK(sdkClient)

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

	// Git persistence.
	router.POST("/workspaces/:id/persist", h.PersistWorkspace)

	// Volume management.
	router.GET("/volumes", h.ListVolumes)
	router.DELETE("/volumes/:name", h.DeleteVolume)

	// Discord slash command handlers.
	router.POST("/discord-command/workspace/list", h.DiscordCommandWorkspaceList)
	router.POST("/discord-command/workspace/create", h.DiscordCommandWorkspaceCreate)
	router.POST("/discord-command/workspace/rename", h.DiscordCommandWorkspaceRename)

	// Tool interface for AI agents.
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/list_environments", h.ToolListEnvironments)
	router.POST("/mcp/create_workspace", h.ToolCreateWorkspace)
	router.POST("/mcp/list_workspaces", h.ToolListWorkspaces)
	router.POST("/mcp/start_workspace", h.ToolStartWorkspace)
	router.POST("/mcp/rename_workspace", h.ToolRenameWorkspace)
	router.POST("/mcp/build_plugin", h.ToolBuildPlugin)
	router.POST("/mcp/deploy_plugin", h.ToolDeployPlugin)
	router.POST("/mcp/promote_plugin", h.ToolPromotePlugin)
	router.POST("/mcp/rollback_plugin", h.ToolRollbackPlugin)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
