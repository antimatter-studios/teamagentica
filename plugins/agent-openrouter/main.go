package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openrouter/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK infrastructure config from env.
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		Dependencies: pluginsdk.PluginDependencies{Capabilities: manifest.Dependencies},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	// Start SDK first (register + heartbeat + event server).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	apiKey := pluginConfig["OPENROUTER_API_KEY"]
	model := pluginConfig["OPENROUTER_MODEL"]
	if model == "" {
		model = "google/gemini-2.5-flash"
	}
	dataPath := pluginConfig["OPENROUTER_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	port := defaultPort
	if portStr := pluginConfig["AGENT_OPENROUTER_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	router := gin.Default()

	h := handlers.NewHandler(apiKey, model, dataPath, debug)
	h.SetSDK(sdkClient)

	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandlerFromManifest(manifest, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// Apply config updates in-place without restarting the container.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		h.ApplyConfig(p.Config)
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
