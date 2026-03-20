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
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	port := 8081
	if v := os.Getenv("AGENT_OPENAI_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         port,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		Dependencies: pluginsdk.PluginDependencies{Capabilities: manifest.Dependencies},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
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
	backend := configOrDefault(pluginConfig, "OPENAI_BACKEND", "subscription")
	apiKey := pluginConfig["OPENAI_API_KEY"]
	model := configOrDefault(pluginConfig, "OPENAI_MODEL", "gpt-4o")
	endpoint := configOrDefault(pluginConfig, "OPENAI_API_ENDPOINT", "https://api.openai.com/v1")
	dataPath := configOrDefault(pluginConfig, "CODEX_DATA_PATH", "/data")
	cliBinary := configOrDefault(pluginConfig, "CODEX_CLI_BINARY", "/usr/local/bin/codex")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	toolLoopLimit := 20
	if v := pluginConfig["TOOL_LOOP_LIMIT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			toolLoopLimit = n
		}
	}

	cliTimeout := 300
	if v := pluginConfig["CODEX_CLI_TIMEOUT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cliTimeout = n
		}
	}

	// When using Codex subscription and model is still the default, switch to
	// the default Codex-compatible model.
	if backend == "subscription" && model == "gpt-4o" {
		model = "gpt-5.3-codex"
	}

	// Set up Gin router.
	router := gin.Default()

	// Create handler with config.
	h := handlers.NewHandler(handlers.HandlerConfig{
		Backend:       backend,
		APIKey:        apiKey,
		Model:         model,
		Endpoint:      endpoint,
		ToolLoopLimit: toolLoopLimit,
		Debug:         debug,
		DataPath:      dataPath,
	})

	// Initialise the appropriate backend.
	if backend == "subscription" {
		log.Println("[subscription] initialising Codex CLI backend")
		workdir := dataPath + "/codex-workspace"
		codexHome := dataPath + "/codex-home"
		dirOK := true
		for _, dir := range []string{workdir, codexHome} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("WARNING: failed to create directory %s: %v (subscription backend may not work)", dir, err)
				dirOK = false
			}
		}

		if !dirOK {
			log.Printf("WARNING: skipping subscription backend init due to directory creation failure")
		} else {
			cliClient := codexcli.NewClient(cliBinary, workdir, codexHome, cliTimeout, debug)
			h.SetCodexCLI(cliClient)

			if cliClient.IsAuthenticated() {
				log.Println("[subscription] Codex CLI is authenticated")
			} else {
				log.Println("[subscription] WARNING: Codex CLI is NOT authenticated — run 'codex login --device-auth' in the container")
			}
		}
	}

	// Register routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.GET("/tools", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)

	// Config options route (dynamic select fields).
	router.GET("/config/options/:field", h.ConfigOptions)

	// Usage tracking.
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Auth routes (always registered; handlers return 400 if Codex not enabled).
	router.GET("/auth/status", h.AuthStatus)
	router.POST("/auth/device-code", h.AuthDeviceCode)
	router.POST("/auth/poll", h.AuthPoll)
	router.DELETE("/auth", h.AuthLogout)

	// Subscribe to MCP server events.
	if backend == "subscription" {
		codexHome := dataPath + "/codex-home"

		sdkClient.OnEvent("mcp_server:enabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			var detail struct {
				Endpoint string `json:"endpoint"`
			}
			if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
				log.Printf("[mcp] failed to parse mcp_server:enabled detail: %v", err)
				return
			}
			if detail.Endpoint == "" {
				log.Printf("[mcp] mcp_server:enabled event has no endpoint")
				return
			}
			if err := codexcli.WriteMCPConfig(codexHome, detail.Endpoint); err != nil {
				log.Printf("[mcp] failed to write MCP config: %v", err)
			}
		}))

		sdkClient.OnEvent("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			if err := codexcli.RemoveMCPConfig(codexHome); err != nil {
				log.Printf("[mcp] failed to remove MCP config: %v", err)
			}
		}))
	}

	h.SetSDK(sdkClient)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "openai", Model: "gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00, CachedPer1M: 1.25, Currency: "USD"},
		{Provider: "openai", Model: "gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60, CachedPer1M: 0.075, Currency: "USD"},
		{Provider: "openai", Model: "o4-mini", InputPer1M: 1.10, OutputPer1M: 4.40, CachedPer1M: 0.275, Currency: "USD"},
		{Provider: "openai", Model: "gpt-5.1-codex", InputPer1M: 1.25, OutputPer1M: 10.00, CachedPer1M: 0.625, Currency: "USD"},
	}, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// Run server with graceful shutdown.
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
