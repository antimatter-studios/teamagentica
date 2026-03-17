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
	"github.com/antimatter-studios/teamagentica/plugins/agent-requesty/internal/handlers"
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
		ConfigSchema: manifest.ConfigSchema,
	})

	// Start SDK first (register + heartbeat + event server).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	apiKey := pluginConfig["REQUESTY_API_KEY"]
	model := pluginConfig["REQUESTY_MODEL"]
	if model == "" {
		model = "google/gemini-2.5-flash"
	}
	dataPath := pluginConfig["REQUESTY_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	port := defaultPort
	if portStr := pluginConfig["AGENT_REQUESTY_PORT"]; portStr != "" {
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
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "requesty", Model: "google/gemini-2.5-flash", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "requesty", Model: "openai/gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00, CachedPer1M: 1.25, Currency: "USD"},
		{Provider: "requesty", Model: "anthropic/claude-sonnet-4-20250514", InputPer1M: 3.00, OutputPer1M: 15.00, CachedPer1M: 0.30, Currency: "USD"},
		{Provider: "requesty", Model: "google/gemini-2.5-pro", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
		{Provider: "requesty", Model: "openai/gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.075, Currency: "USD"},
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
