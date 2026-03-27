package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-alias-registry/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-alias-registry/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

	h := handlers.New(nil, nil) // pre-create for ToolsFunc; reassigned after DB init

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
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

	port := defaultPort
	dataPath := "/data"

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	router := gin.Default()
	h = handlers.New(db, sdkClient)

	// Health
	router.GET("/health", h.Health)

	// Alias REST API
	router.GET("/alias/:name", h.GetAlias)
	router.GET("/aliases", h.ListAliases)
	router.POST("/aliases", h.CreateAlias)
	router.PUT("/aliases/:name", h.UpdateAlias)
	router.DELETE("/aliases/:name", h.DeleteAlias)

	// Backward-compatible persona endpoint (used by relay)
	router.GET("/persona/:alias", h.GetPersona)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_aliases", h.MCPListAliases)
	router.POST("/mcp/get_alias", h.MCPGetAlias)
	router.POST("/mcp/create_alias", h.MCPCreateAlias)
	router.POST("/mcp/update_alias", h.MCPUpdateAlias)
	router.POST("/mcp/delete_alias", h.MCPDeleteAlias)
	router.POST("/mcp/migrate_from_kernel", h.MCPMigrateFromKernel)

	// Migration
	router.POST("/migrate-from-kernel", h.MigrateFromKernel)

	// Signal that the registry is ready so plugins that started before us can re-fetch.
	sdkClient.ReportEvent("alias-registry:ready", "")

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
