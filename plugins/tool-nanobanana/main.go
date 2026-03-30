package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-nanobanana/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8081

	var h *handlers.Handler

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
		ToolsFunc: func() interface{} {
			if h != nil {
				return h.ToolDefs()
			}
			return nil
		},
	})
	sdkClient.Start(context.Background())

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	apiKey := pluginConfig["GEMINI_API_KEY"]
	model := pluginConfig["NANOBANANA_MODEL"]
	if model == "" {
		model = "gemini-2.5-flash-image"
	}
	dataPath := pluginConfig["NANOBANANA_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	port := defaultPort
	if portStr := pluginConfig["TOOL_NANOBANANA_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	router := gin.Default()
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))

	h = handlers.NewHandler(apiKey, model, dataPath, debug)
	h.SetSDK(sdkClient)

	router.GET("/health", h.Health)
	router.GET("/models", h.Models)
	router.POST("/generate", h.Generate)
	router.POST("/chat", h.Chat)
	router.GET("/mcp", h.Tools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

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

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "nanobanana", Model: "gemini-2.5-flash-image", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "nanobanana", Model: "gemini-3.1-flash-image-preview", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.0375, Currency: "USD"},
		{Provider: "nanobanana", Model: "gemini-3-pro-image-preview", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.3125, Currency: "USD"},
	}, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// Push tools to MCP server when it becomes available.
	sdkClient.WhenPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

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
