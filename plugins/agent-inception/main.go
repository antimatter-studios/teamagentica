package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/handlers"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK infrastructure config from env.
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

	// Start SDK first (register + heartbeat + event server).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	apiKey := pluginConfig["INCEPTION_API_KEY"]
	model := pluginConfig["INCEPTION_MODEL"]
	if model == "" {
		model = "mercury-2"
	}
	endpoint := pluginConfig["INCEPTION_API_ENDPOINT"]
	if endpoint == "" {
		endpoint = "https://api.inceptionlabs.ai/v1"
	}
	dataPath := pluginConfig["INCEPTION_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	diffusing := pluginConfig["INCEPTION_DIFFUSING"] == "true"
	instant := pluginConfig["INCEPTION_INSTANT"] == "true"

	toolLoopLimit := 20
	if v := pluginConfig["TOOL_LOOP_LIMIT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			toolLoopLimit = n
		}
	}

	port := defaultPort
	if portStr := pluginConfig["AGENT_INCEPTION_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	router := gin.Default()

	h := handlers.NewHandler(apiKey, model, endpoint, dataPath, debug, diffusing, instant, toolLoopLimit, defaultSystemPrompt)
	h.SetSDK(sdkClient)

	// Standard agent routes.
	router.GET("/health", h.Health)
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))
	router.POST("/chat", h.Chat)
	router.GET("/mcp", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)

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

	// Inception-specific code editing endpoints.
	router.POST("/apply-edit", h.ApplyEdit)
	router.POST("/next-edit", h.NextEdit)
	router.POST("/fim", h.FIM)

	// Usage tracking.
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "inception", Model: "mercury-2", InputPer1M: 0.25, OutputPer1M: 0.75, Currency: "USD"},
		{Provider: "inception", Model: "mercury-coder-small", InputPer1M: 0.25, OutputPer1M: 0.75, Currency: "USD"},
		{Provider: "inception", Model: "mercury-edit", InputPer1M: 0.25, OutputPer1M: 0.75, Currency: "USD"},
	}, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
