package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/provider"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8085

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

	sdkClient.Start(context.Background())

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	apiKey := pluginConfig["INCEPTION_API_KEY"]
	model := configOrDefault(pluginConfig, "INCEPTION_MODEL", "mercury-2")
	endpoint := configOrDefault(pluginConfig, "INCEPTION_API_ENDPOINT", "https://api.inceptionlabs.ai/v1")
	dataPath := configOrDefault(pluginConfig, "INCEPTION_DATA_PATH", "/data")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	diffusing := pluginConfig["INCEPTION_DIFFUSING"] == "true"
	instant := pluginConfig["INCEPTION_INSTANT"] == "true"

	toolLoopLimit := 20
	if v := pluginConfig["TOOL_LOOP_LIMIT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			toolLoopLimit = n
		}
	}

	// Create the plugin-specific handler (apply-edit, next-edit, FIM, usage, models, config).
	h := handlers.NewHandler(handlers.HandlerConfig{
		APIKey:        apiKey,
		Model:         model,
		Endpoint:      endpoint,
		DataPath:      dataPath,
		Debug:         debug,
		Diffusing:     diffusing,
		Instant:       instant,
		ToolLoopLimit: toolLoopLimit,
		DefaultPrompt: defaultSystemPrompt,
	})
	h.SetSDK(sdkClient)

	// Create the agentkit adapter.
	adapter := provider.NewAdapter(provider.AdapterConfig{
		APIKey:    apiKey,
		Model:     model,
		Endpoint:  endpoint,
		Diffusing: diffusing,
		Instant:   instant,
		Debug:     debug,
		Tracker:   h.Tracker(),
	})

	router := gin.Default()

	// Register core agent routes via agentkit (/chat, /health, /mcp).
	agentkit.RegisterAgentChat(router, sdkClient, adapter, defaultSystemPrompt,
		agentkit.WithDefaultModel(model),
		agentkit.WithMaxTokens(4096),
		agentkit.WithMaxToolLoops(toolLoopLimit),
		agentkit.WithDebug(debug),
	)

	// Plugin-specific routes (not handled by agentkit).
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)

	// Inception-specific code editing endpoints.
	router.POST("/apply-edit", h.ApplyEdit)
	router.POST("/next-edit", h.NextEdit)
	router.POST("/fim", h.FIM)

	// Usage tracking.
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Apply config updates in-place without restarting the container.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		h.ApplyConfig(p.Config)
		adapter.ApplyConfig(p.Config)
	})

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandlerFromManifest(manifest, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	sdkClient.ListenAndServe(defaultPort, router)
}

func configOrDefault(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return fallback
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
