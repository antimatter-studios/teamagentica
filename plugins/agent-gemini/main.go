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
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "agent-gemini"
	}

	port := 8081
	if v := os.Getenv("AGENT_GEMINI_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         getHostname(),
		Port:         port,
		Capabilities: []string{"ai:chat", "ai:chat:gemini"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"GEMINI_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://aistudio.google.com/apikey", Order: 1},
			"GEMINI_MODEL":   {Type: "select", Label: "Model", Default: "gemini-2.5-flash", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES": {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":   {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
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
		APIKey:   apiKey,
		Model:    model,
		Debug:    debug,
		DataPath: dataPath,
	})
	h.SetSDK(sdkClient)

	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
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

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
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
