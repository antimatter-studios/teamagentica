package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/handlers"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
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

	// Start SDK first (register with kernel + heartbeat loop + event server).
	ctx := context.Background()
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	apiKey := pluginConfig["GEMINI_API_KEY"]
	model := configOrDefault(pluginConfig, "GEMINI_MODEL", "gemini-2.5-flash")
	dataPath := configOrDefault(pluginConfig, "GEMINI_DATA_PATH", "/data")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	router := gin.Default()

	h := handlers.NewHandler(handlers.HandlerConfig{
		APIKey:              apiKey,
		Model:               model,
		Debug:               debug,
		DataPath:            dataPath,
		DefaultSystemPrompt: defaultSystemPrompt,
	})
	h.SetSDK(sdkClient)

	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.POST("/chat/stream", h.ChatStream)
	router.GET("/mcp", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "gemini", Model: "gemini-2.5-flash", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "gemini", Model: "gemini-2.5-pro", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
		{Provider: "gemini", Model: "gemini-2.0-flash", InputPer1M: 0.10, OutputPer1M: 0.40, CachedPer1M: 0.025, Currency: "USD"},
	}, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// OpenAI-compatible proxy — allows other plugins (e.g. infra-agent-memory)
	// to use Gemini models via standard OpenAI endpoints without their own API key.
	router.Any("/v1/*path", h.OpenAIProxy)

	// Apply config updates in-place without restarting the container.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("[config] failed to parse config:update detail: %v", err)
			return
		}
		h.ApplyConfig(detail.Config)
	}))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
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
