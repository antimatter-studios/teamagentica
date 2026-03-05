package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/handlers"
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
		Capabilities: []string{"ai:chat", "ai:chat:kimi"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"KIMI_API_KEY":   {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.moonshot.ai", Order: 1},
			"KIMI_MODEL":     {Type: "select", Label: "Model", Default: "kimi-k2-turbo-preview", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES": {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":   {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "moonshot", Model: "kimi-k2-turbo-preview", InputPer1M: 1.15, OutputPer1M: 8.00, CachedPer1M: 0.29, Currency: "USD"},
		{Provider: "moonshot", Model: "kimi-k2.5", InputPer1M: 0.60, OutputPer1M: 3.00, CachedPer1M: 0.15, Currency: "USD"},
		{Provider: "moonshot", Model: "kimi-k2-0905-preview", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
		{Provider: "moonshot", Model: "kimi-k2-0711-preview", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
		{Provider: "moonshot", Model: "kimi-k2-thinking", InputPer1M: 0.60, OutputPer1M: 2.50, CachedPer1M: 0.15, Currency: "USD"},
		{Provider: "moonshot", Model: "kimi-k2-thinking-turbo", InputPer1M: 1.15, OutputPer1M: 8.00, CachedPer1M: 0.29, Currency: "USD"},
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
