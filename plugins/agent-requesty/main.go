package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-requesty/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/agent-requesty/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	router := gin.Default()

	h := handlers.NewHandler(cfg)

	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"ai:chat", "ai:chat:requesty"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"REQUESTY_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://app.requesty.ai", Order: 1},
			"REQUESTY_MODEL":  {Type: "select", Label: "Model", Default: "google/gemini-2.5-flash", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":  {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":    {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

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
		Addr:    fmt.Sprintf(":%d", cfg.Port),
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
