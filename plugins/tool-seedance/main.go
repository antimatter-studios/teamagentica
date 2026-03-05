package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-seedance/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/tool-seedance/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	router := gin.Default()

	h := handlers.NewHandler(cfg)

	router.GET("/health", h.Health)
	router.POST("/generate", h.Generate)
	router.GET("/status/:taskId", h.Status)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"tool:video", "tool:video:seedance"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"SEEDANCE_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key from the Dreamina developer portal", Order: 1},
			"SEEDANCE_MODEL":  {Type: "select", Label: "Model", Default: "seedance-2.0", Dynamic: true, Order: 2},
			"PLUGIN_DEBUG":    {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "seedance", Model: "seedance-2.0", PerRequest: 0.03, Currency: "USD"},
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
