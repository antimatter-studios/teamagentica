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
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "infra-workspace-manager"
	}

	const defaultPort = 8091

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"workspace:manager"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"WORKSPACE_MANAGER_PORT": {Type: "number", Label: "Listen Port", Default: "8091", HelpText: "Port the workspace manager listens on"},
			"WORKSPACE_DIR":          {Type: "string", Label: "Workspace Directory", Default: "/workspaces", HelpText: "Base directory for workspaces"},
			"PLUGIN_DEBUG":           {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed operations", Order: 99},
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
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

	workspaceDir := pluginConfig["WORKSPACE_DIR"]
	if workspaceDir == "" {
		workspaceDir = "/workspaces"
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	// Ensure workspace directory exists.
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		log.Fatalf("failed to create workspace dir %s: %v", workspaceDir, err)
	}

	router := gin.Default()
	h := handlers.NewHandler(workspaceDir, debug)

	router.GET("/health", h.Health)
	router.GET("/workspaces", h.ListWorkspaces)
	router.POST("/workspaces", h.CreateWorkspace)
	router.GET("/workspaces/:id", h.WorkspaceStatus)
	router.DELETE("/workspaces/:id", h.DeleteWorkspace)
	router.POST("/workspaces/:id/persist", h.PersistWorkspace)

	h.SetSDK(sdkClient)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
