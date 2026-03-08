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
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK infrastructure config from env.
	sdkCfg := pluginsdk.LoadConfig()

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "agent-inception"
	}

	const defaultPort = 8085

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: []string{"ai:chat", "ai:chat:inception"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"INCEPTION_API_KEY":   {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.inceptionlabs.ai/", Order: 1},
			"INCEPTION_MODEL":    {Type: "select", Label: "Model", Default: "mercury-2", Dynamic: true, Order: 2},
			"INCEPTION_INSTANT":  {Type: "boolean", Label: "Instant Mode", Default: "false", HelpText: "Use reasoning_effort=instant for lowest latency responses (reduced quality)", Order: 3},
			"INCEPTION_DIFFUSING": {Type: "boolean", Label: "Diffusing Mode", Default: "false", HelpText: "Visualise the diffusion denoising process in streaming responses", Order: 4},
			"TOOL_LOOP_LIMIT":    {Type: "string", Label: "Tool Loop Limit", Default: "20", HelpText: "Maximum tool-calling iterations per request. Set to 0 for unrestricted.", Order: 10},
			"PLUGIN_ALIASES":     {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin. Each alias maps a short name to a plugin:model target.", Order: 90},
			"PLUGIN_DEBUG":       {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
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

	h := handlers.NewHandler(apiKey, model, endpoint, dataPath, debug, diffusing, instant, toolLoopLimit)
	h.SetSDK(sdkClient)

	// Standard agent routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)

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
