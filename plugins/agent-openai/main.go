package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/provider"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
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

	// Create plugin-specific handler (auth, usage, models, config options).
	h := handlers.NewHandler(handlers.HandlerConfig{
		Backend:             backend,
		APIKey:              apiKey,
		Model:               model,
		Endpoint:            endpoint,
		ToolLoopLimit:       toolLoopLimit,
		Debug:               debug,
		DataPath:            dataPath,
		DefaultSystemPrompt: defaultSystemPrompt,
	})

	// Create the agentkit adapter.
	adapter := provider.NewAdapter(provider.AdapterConfig{
		Backend:  backend,
		APIKey:   apiKey,
		Model:    model,
		Endpoint: endpoint,
		Debug:    debug,
		Tracker:  h.Tracker(),
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
			if sdkCfg.TLSCA != "" {
				cliClient.SetTLS(sdkCfg.TLSCA)
				log.Printf("[cli] TLS CA configured for Codex CLI subprocess")
			}

			// Attach CLI client to both adapter and handler (handler needs it for auth).
			adapter.SetCodexCLI(cliClient)
			h.SetCodexCLI(cliClient)

			if cliClient.IsAuthenticated() {
				log.Println("[subscription] Codex CLI is authenticated")
			} else {
				log.Println("[subscription] WARNING: Codex CLI is NOT authenticated — run 'codex login --device-auth' in the container")
			}

			// Test: start app-server at boot to see if it blocks.
			log.Println("[app-server] starting test...")
			if err := cliClient.StartAppServer(); err != nil {
				log.Printf("[app-server] start failed: %v", err)
			} else {
				log.Println("[app-server] started OK")
			}
		}
	}

	// Set up Gin router.
	router := gin.Default()

	// Wire event emission from the adapter to the SDK.
	h.SetSDK(sdkClient)
	adapter.SetEmitEvent(func(eventType, detail string) {
		sdkClient.PublishEvent(eventType, detail)
	})

	// Register core agent routes via agentkit (/chat, /health, /mcp).
	agentkit.RegisterAgentChat(router, sdkClient, adapter, defaultSystemPrompt,
		agentkit.WithDefaultModel(model),
		agentkit.WithMaxToolLoops(toolLoopLimit),
		agentkit.WithDebug(debug),
	)

	// Plugin-specific routes (not handled by agentkit).
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Auth routes (always registered; handlers return 400 if Codex not enabled).
	router.GET("/auth/status", h.AuthStatus)
	router.POST("/auth/device-code", h.AuthDeviceCode)
	router.POST("/auth/poll", h.AuthPoll)
	router.DELETE("/auth", h.AuthLogout)

	// Apply config updates in-place without restarting the container.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		h.ApplyConfig(p.Config)
		adapter.ApplyConfig(p.Config)
	})

	// MCP bridge: localhost plain HTTP proxy -> infra-mcp-server via mTLS.
	// Codex CLI (which can't present client certs) connects to this proxy.
	if backend == "subscription" {
		codexHome := dataPath + "/codex-home"

		// Helper to start proxy and configure Codex CLI.
		configureMCP := func(mcpPluginID string) {
			proxy, err := sdkClient.StartMCPBridge(mcpPluginID)
			if err != nil {
				log.Printf("[mcp] failed to start MCP proxy: %v", err)
				return
			}
			if err := codexcli.WriteMCPConfig(codexHome, proxy.URL); err != nil {
				log.Printf("[mcp] failed to write MCP config: %v", err)
			} else {
				log.Printf("[mcp] configured MCP proxy -> %s (plugin=%s)", proxy.URL, mcpPluginID)
			}
		}

		// Proactively discover MCP server at startup.
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

		// Hot-reload when MCP server restarts.
		sdkClient.Events().On("mcp_server:enabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			configureMCP("infra-mcp-server")
		}))

		sdkClient.Events().On("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			if err := codexcli.RemoveMCPConfig(codexHome); err != nil {
				log.Printf("[mcp] failed to remove MCP config: %v", err)
			}
			log.Printf("[mcp] MCP server disabled, config removed")
		}))
	}

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandlerFromManifest(manifest, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	sdkClient.ListenAndServe(defaultPort, router)
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
