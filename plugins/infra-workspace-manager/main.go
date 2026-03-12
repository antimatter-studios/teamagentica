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
			"PLUGIN_DEBUG":           {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed operations", Order: 99},
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
	baseDomain := os.Getenv("TEAMAGENTICA_BASE_DOMAIN")

	// Workspace directory — contains volumes/ subdirectory for workspace data.
	workspaceDir := "/workspaces"
	if err := os.MkdirAll(workspaceDir+"/volumes", 0755); err != nil {
		log.Fatalf("failed to create workspace volumes dir: %v", err)
	}

	// Local SQLite database for workspace-manager-level metadata
	// (environment tracking, etc.) — kept separate from the kernel.
	db, err := storage.Open(workspaceDir)
	if err != nil {
		log.Fatalf("failed to open workspace database: %v", err)
	}

	router := gin.Default()
	h := handlers.NewHandler(workspaceDir, baseDomain, debug, db)
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

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
