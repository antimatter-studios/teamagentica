package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/handlers"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8082

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

	poolMax := 10
	if v := pluginConfig["CLAUDE_POOL_MAX"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			poolMax = n
		}
	}
	if poolMax < 2 {
		poolMax = 2
	}

	poolTTL := 120
	if v := pluginConfig["CLAUDE_POOL_TTL"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 {
			poolTTL = n
		}
	}

	router := gin.Default()

	h := handlers.NewHandler(handlers.HandlerConfig{
		Backend:             backend,
		APIKey:              apiKey,
		Model:               model,
		Debug:               debug,
		DataPath:            dataPath,
		WorkspaceDir:        workspaceDir,
		DefaultSystemPrompt: defaultSystemPrompt,
	})

	// Initialise the CLI backend if configured.
	if backend == "cli" {
		log.Println("[cli] initialising Claude CLI backend")
		workdir := dataPath + "/claude-workspace"
		claudeDir := dataPath + "/claude-home"
		dirOK := true
		for _, dir := range []string{workdir, claudeDir} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("WARNING: failed to create directory %s: %v (CLI backend may not work)", dir, err)
				dirOK = false
			}
		}

		if !dirOK {
			log.Printf("WARNING: skipping CLI backend init due to directory creation failure")
		} else {
			cliClient := claudecli.NewClient(cliBinary, workdir, claudeDir, cliTimeout, debug)
			cliClient.SetPoolMax(poolMax)
			cliClient.SetPoolTTL(poolTTL)
			if sdkCfg.TLSCert != "" {
				cliClient.SetTLS(sdkCfg.TLSCert, sdkCfg.TLSKey, sdkCfg.TLSCA)
				log.Printf("[cli] mTLS configured for Claude CLI subprocess")
			}
			if pluginConfig["CLAUDE_SKIP_PERMISSIONS"] == "true" {
				cliClient.SetSkipPermissions(true)
				log.Println("[cli] skip-permissions enabled — all tools auto-approved")
			}
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
	}

	// Register routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.POST("/chat/stream", h.ChatStream)
	router.GET("/mcp", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// MCP proxy: forward requests to infra-mcp-server via SDK mTLS.
	router.Any("/mcp-proxy", gin.WrapF(h.MCPProxyRaw))

	// Auth routes (proxied via kernel /api/route/:id/auth/*).
	router.GET("/auth/status", h.AuthStatus)
	router.POST("/auth/device-code", h.AuthDeviceCode)
	router.POST("/auth/submit-code", h.AuthSubmitCode)
	router.DELETE("/auth", h.AuthLogout)

	// Apply config updates in-place without restarting the container.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		h.ApplyConfig(p.Config)
	})

	// MCP server integration: CLI subprocess connects to the plugin's own
	// /mcp-proxy route on localhost, which forwards to infra-mcp-server via SDK mTLS.
	if backend == "cli" {
		claudeDir := dataPath + "/claude-home"
		proxyURL := fmt.Sprintf("https://localhost:%d/mcp-proxy", defaultPort)

		// Helper to configure MCP when the server is discovered.
		configureMCP := func(mcpPluginID string) {
			h.SetMCPPluginID(mcpPluginID)
			if err := claudecli.WriteMCPConfig(claudeDir, proxyURL); err != nil {
				log.Printf("[mcp] failed to write MCP config: %v", err)
			} else {
				h.SetMCPConfig(claudecli.MCPConfigPath(claudeDir))
				log.Printf("[mcp] configured MCP proxy → %s (plugin=%s)", proxyURL, mcpPluginID)
			}
		}

		// Proactively discover MCP server at startup (don't wait for event).
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

		// Also subscribe to events for hot-reload when MCP server restarts.
		sdkClient.Events().On("mcp_server:enabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			var detail struct {
				Endpoint string `json:"endpoint"`
			}
			if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
				log.Printf("[mcp] failed to parse mcp_server:enabled detail: %v", err)
				return
			}
			// Extract plugin ID from endpoint hostname (e.g. "http://teamagentica-plugin-infra-mcp-server:8081/mcp").
			configureMCP("infra-mcp-server")
		}))

		sdkClient.Events().On("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			if err := claudecli.RemoveMCPConfig(claudeDir); err != nil {
				log.Printf("[mcp] failed to remove MCP config: %v", err)
			}
			h.SetMCPConfig("")
			h.SetMCPPluginID("")
			log.Printf("[mcp] MCP server disabled, config removed")
		}))
	}

	h.SetSDK(sdkClient)

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
