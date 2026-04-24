package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8081

	var h *handlers.Handler

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
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
	sdkClient.Start(context.Background())

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	apiKey := pluginConfig["GEMINI_API_KEY"]

	model := pluginConfig["NANOBANANA_MODEL"]
	if model == "" {
		model = "gemini-2.5-flash-image"
	}

	dataPath := pluginConfig["NANOBANANA_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}

	port := defaultPort
	if portStr := pluginConfig["TOOL_NANOBANANA_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	router := gin.Default()

	h = handlers.NewHandler(apiKey, model, dataPath, debug)
	h.SetSDK(sdkClient)

	router.GET("/health", h.Health)
	router.GET("/models", h.Models)
	router.POST("/generate", h.Generate)
	router.POST("/chat", h.Chat)
	router.GET("/mcp", h.Tools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Pricing endpoints — defaults loaded from plugin.yaml default_pricing.
	pricing := pluginsdk.NewPricingHandlerFromManifest(manifest, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// Apply config updates in-place without restarting the container.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		h.ApplyConfig(p.Config)
	})

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(port, router)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
