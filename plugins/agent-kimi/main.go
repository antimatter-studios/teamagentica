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
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/kimicli"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

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

	apiKey := pluginConfig["KIMI_API_KEY"]
	backend := configOrDefault(pluginConfig, "KIMI_BACKEND", "api_key")
	model := configOrDefault(pluginConfig, "KIMI_MODEL", "kimi-k2-turbo-preview")
	dataPath := configOrDefault(pluginConfig, "KIMI_DATA_PATH", "/data")
	cliBinary := configOrDefault(pluginConfig, "KIMI_CLI_BINARY", "/usr/local/bin/kimi")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	cliTimeout := 300
	if v := pluginConfig["KIMI_CLI_TIMEOUT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cliTimeout = n
		}
	}

	port := defaultPort
	if portStr := pluginConfig["AGENT_KIMI_PORT"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	router := gin.Default()

	h := handlers.NewHandler(apiKey, model, dataPath, debug, defaultSystemPrompt)
	h.SetSDK(sdkClient)

	// Initialise CLI backend if configured.
	if backend == "cli" {
		log.Println("[cli] initialising Kimi CLI backend")
		kimiHome := dataPath + "/kimi-home"
		workdir := dataPath + "/kimi-workspace"
		for _, dir := range []string{kimiHome, workdir} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("WARNING: failed to create %s: %v", dir, err)
			}
		}

		// Write CLI config with provider credentials.
		if apiKey != "" {
			if err := kimicli.WriteConfig(kimiHome, apiKey, model); err != nil {
				log.Printf("WARNING: failed to write kimi-cli config: %v", err)
			}
		}

		cliClient := kimicli.NewClient(cliBinary, workdir, kimiHome, cliTimeout, debug)
		h.SetKimiCLI(cliClient)

		// MCP bridge: localhost HTTP proxy → infra-mcp-server via mTLS.
		configureMCP := func(mcpPluginID string) {
			proxy, err := sdkClient.StartMCPBridge(mcpPluginID)
			if err != nil {
				log.Printf("[mcp] failed to start MCP proxy: %v", err)
				return
			}
			configPath, err := kimicli.WriteMCPConfig(kimiHome, proxy.URL)
			if err != nil {
				log.Printf("[mcp] failed to write MCP config: %v", err)
			} else {
				h.SetMCPConfigFile(configPath)
				log.Printf("[mcp] configured MCP proxy → %s (plugin=%s)", proxy.URL, mcpPluginID)
			}
		}

		// Discover MCP server at startup.
		go func() {
			plugins, err := sdkClient.SearchPlugins("infra:mcp-server")
			if err != nil {
				log.Printf("[mcp] startup discovery failed: %v", err)
				return
			}
			for _, p := range plugins {
				if p.ID == "infra-mcp-server" {
					configureMCP(p.ID)
					return
				}
			}
			log.Printf("[mcp] infra-mcp-server not found at startup, waiting for event")
		}()

		// Hot-reload on MCP server events.
		sdkClient.OnEvent("mcp_server:enabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			configureMCP("infra-mcp-server")
		}))

		sdkClient.OnEvent("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			if err := kimicli.RemoveMCPConfig(kimiHome); err != nil {
				log.Printf("[mcp] failed to remove MCP config: %v", err)
			}
			h.SetMCPConfigFile("")
			log.Printf("[mcp] MCP server disabled, config removed")
		}))
	}

	router.GET("/health", h.Health)
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))
	router.POST("/chat", h.Chat)
	router.POST("/chat/stream", h.ChatStream)
	router.GET("/mcp", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
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
