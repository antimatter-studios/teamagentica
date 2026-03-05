package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	router := gin.Default()

	h := handlers.NewHandler(cfg)

	router.GET("/health", h.Health)
	router.POST("/generate", h.Generate)
	router.POST("/chat", h.Chat)
	router.GET("/tools", h.Tools)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"tool:image", "tool:image:nanobanana"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"GEMINI_API_KEY":   {Type: "string", Label: "Gemini API Key", Required: true, Secret: true, HelpText: "Get your API key at https://aistudio.google.com/apikey", Order: 1},
			"NANOBANANA_MODEL": {Type: "select", Label: "Model", Default: "gemini-2.5-flash-image", Dynamic: true, Order: 2},
			"PLUGIN_ALIASES":   {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":     {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "nanobanana", Model: "gemini-2.5-flash-image", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "nanobanana", Model: "gemini-3.1-flash-image-preview", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "nanobanana", Model: "gemini-3-pro-image-preview", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
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
