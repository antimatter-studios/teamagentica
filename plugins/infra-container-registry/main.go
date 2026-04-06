package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-container-registry/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8081

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	hostname, _ := os.Hostname()

	var h *handlers.Handler

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
			if h != nil {
				stats, err := h.RegistryStats()
				if err == nil {
					schema["registry"] = stats
				}
			}
			return schema
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

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	h = handlers.NewHandler(sdkClient, debug)

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	router := gin.Default()

	router.GET("/health", h.Health)

	// Registry catalog API.
	router.GET("/images", h.ListImages)
	router.GET("/images/:name/tags", h.ListTags)
	router.DELETE("/images/:name/:tag", h.DeleteImage)

	// MCP tool endpoints.
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/list_images", h.ToolListImages)
	router.POST("/mcp/list_tags", h.ToolListTags)
	router.POST("/mcp/delete_image", h.ToolDeleteImage)

	sdkClient.ListenAndServe(defaultPort, router)
}
