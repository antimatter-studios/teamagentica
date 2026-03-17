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
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	port := 8082
	if v := os.Getenv("AGENT_CLAUDE_PORT"); v != "" {
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
		ConfigSchema: manifest.ConfigSchema,
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
	backend := configOrDefault(pluginConfig, "CLAUDE_BACKEND", "cli")
	apiKey := pluginConfig["ANTHROPIC_API_KEY"]
	model := configOrDefault(pluginConfig, "CLAUDE_MODEL", "claude-sonnet-4-6")
	dataPath := configOrDefault(pluginConfig, "CLAUDE_DATA_PATH", "/data")
	cliBinary := configOrDefault(pluginConfig, "CLAUDE_CLI_BINARY", "/usr/local/bin/claude")
	workspaceDir := configOrDefault(pluginConfig, "WORKSPACE_DIR", "/workspaces")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	cliTimeout := 600
	if v := pluginConfig["CLAUDE_CLI_TIMEOUT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cliTimeout = n
		}
	}

	router := gin.Default()

	h := handlers.NewHandler(handlers.HandlerConfig{
		Backend:      backend,
		APIKey:       apiKey,
		Model:        model,
		Debug:        debug,
		DataPath:     dataPath,
		WorkspaceDir: workspaceDir,
	})

	// Initialise the CLI backend if configured.
	if backend == "cli" {
		log.Println("[cli] initialising Claude CLI backend")
		workdir := dataPath + "/claude-workspace"
		claudeDir := dataPath + "/claude-home"
		for _, dir := range []string{workdir, claudeDir} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Fatalf("failed to create directory %s: %v", dir, err)
			}
		}

		cliClient := claudecli.NewClient(cliBinary, workdir, claudeDir, cliTimeout, debug)
		h.SetClaudeCLI(cliClient)

		// Set MCP config path if it exists.
		if mcpPath := claudecli.MCPConfigPath(claudeDir); mcpPath != "" {
			h.SetMCPConfig(mcpPath)
		}

		if cliClient.IsAvailable() {
			log.Println("[cli] Claude CLI is available")
		} else {
			log.Println("[cli] WARNING: Claude CLI is NOT available — check CLAUDE_CLI_BINARY path")
		}
	}

	// Register routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.GET("/tools", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Auth routes (proxied via kernel /api/route/:id/auth/*).
	router.GET("/auth/status", h.AuthStatus)
	router.POST("/auth/device-code", h.AuthDeviceCode)
	router.POST("/auth/submit-code", h.AuthSubmitCode)
	router.POST("/auth/poll", h.AuthPoll)
	router.DELETE("/auth", h.AuthLogout)

	// Subscribe to MCP server events.
	if backend == "cli" {
		claudeDir := dataPath + "/claude-home"

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
			if err := claudecli.WriteMCPConfig(claudeDir, detail.Endpoint); err != nil {
				log.Printf("[mcp] failed to write MCP config: %v", err)
			} else {
				h.SetMCPConfig(claudecli.MCPConfigPath(claudeDir))
			}
		}))

		sdkClient.OnEvent("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			if err := claudecli.RemoveMCPConfig(claudeDir); err != nil {
				log.Printf("[mcp] failed to remove MCP config: %v", err)
			}
			h.SetMCPConfig("")
		}))
	}

	h.SetSDK(sdkClient)

	// Pricing endpoints.
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "anthropic", Model: "claude-opus-4-6", InputPer1M: 15.00, OutputPer1M: 75.00, CachedPer1M: 1.875, Currency: "USD"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6", InputPer1M: 3.00, OutputPer1M: 15.00, CachedPer1M: 0.375, Currency: "USD"},
		{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", InputPer1M: 0.80, OutputPer1M: 4.00, CachedPer1M: 0.08, Currency: "USD"},
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
