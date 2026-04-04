package main

import (
	"context"
	"log"
	"os"
	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-builder/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

	var h *handlers.Handler

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
			if h != nil {
				return h.ToolDefs()
			}
			return nil
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	port := defaultPort

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	router := gin.Default()

	h = handlers.NewHandler(sdkClient, debug)

	router.GET("/health", h.Health)
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/build", h.ToolBuild)
	router.POST("/build", h.Build)
	router.GET("/builds", h.ListBuilds)
	router.GET("/builds/:id/logs", h.GetBuildLogs)

	sdkClient.ListenAndServe(port, router)
}
