package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/agentkit"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/exec"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/agent-anthropic/internal/provider"
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
	execMode := configOrDefault(pluginConfig, "CLAUDE_EXEC_MODE", "local")
	execWSURL := pluginConfig["CLAUDE_EXEC_WS_URL"]
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

	// Create the plugin-specific handler (auth, MCP proxy, usage, models, config).
	h := handlers.NewHandler(handlers.HandlerConfig{
		Backend:             backend,
		APIKey:              apiKey,
		Model:               model,
		Debug:               debug,
		DataPath:            dataPath,
		DefaultSystemPrompt: defaultSystemPrompt,
	})

	// Create the agentkit adapter.
	adapter := provider.NewAdapter(provider.AdapterConfig{
		Model:         model,
		Debug:         debug,
		DefaultPrompt: defaultSystemPrompt,
		WorkspaceDir:  workspaceDir,
		ExecMode:      execMode,
		ExecWSURL:     execWSURL,
		Tracker:       h.Tracker(),
	})

	if execMode == "remote" {
		log.Printf("[remote] exec mode enabled: %s", execWSURL)
	}

	// Initialise the CLI backend if configured (skip in remote mode — CLI runs in workspace).
	if backend == "cli" && execMode != "remote" {
		log.Println("[cli] initialising Claude CLI backend")
		workdir := dataPath + "/claude-workspace"
		claudeDir := configOrDefault(pluginConfig, "CLAUDE_CONFIG_DIR", "/home/coder/.claude")
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

			// Attach CLI client to both the adapter and handler (handler needs it for auth).
			adapter.SetClaudeCLI(cliClient)
			h.SetClaudeCLI(cliClient)

			// Set MCP config path if it exists.
			if mcpPath := claudecli.MCPConfigPath(claudeDir); mcpPath != "" {
				adapter.SetMCPConfig(mcpPath)
			}

			if cliClient.IsAvailable() {
				log.Println("[cli] Claude CLI is available")
			} else {
				log.Println("[cli] WARNING: Claude CLI is NOT available — check CLAUDE_CLI_BINARY path")
			}
		}
	}

	router := gin.Default()

	// Wire event emission from the adapter to the SDK.
	h.SetSDK(sdkClient)
	adapter.SetEmitEvent(func(eventType, detail string) {
		sdkClient.PublishEvent(eventType, detail)
	})

	// Register core agent routes via agentkit (/chat, /health, /mcp).
	agentkit.RegisterAgentChat(router, sdkClient, adapter, defaultSystemPrompt,
		agentkit.WithDefaultModel(model),
		agentkit.WithMaxTokens(8192),
		agentkit.WithDebug(debug),
	)

	// Plugin-specific routes (not handled by agentkit).
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
		adapter.ApplyConfig(p.Config)
	})

	// MCP server integration: CLI subprocess connects directly to infra-mcp-server
	// via mTLS. Direct connection allows the mcp-go server to push tools/list_changed
	// notifications to Claude CLI when tools are added or removed.
	if backend == "cli" {
		claudeDir := configOrDefault(pluginConfig, "CLAUDE_CONFIG_DIR", "/home/coder/.claude")
		const mcpPlugin = "infra-mcp-server"

		enableMCP := func(host string, port int) {
			sdkClient.SetPeer(mcpPlugin, host, port)
			h.SetMCPPluginID(mcpPlugin)
			directURL := fmt.Sprintf("https://%s:%d/mcp", host, port)
			if err := claudecli.WriteMCPConfig(claudeDir, directURL); err != nil {
				log.Printf("[mcp] enable: failed to write config: %v", err)
				return
			}
			adapter.SetMCPConfig(claudecli.MCPConfigPath(claudeDir))
			log.Printf("[mcp] enabled: direct=%s", directURL)
		}

		disableMCP := func() {
			sdkClient.InvalidatePeer(mcpPlugin)
			h.SetMCPPluginID("")
			adapter.SetMCPConfig("")
			if err := claudecli.RemoveMCPConfig(claudeDir); err != nil {
				log.Printf("[mcp] disable: failed to remove config: %v", err)
			}
			log.Printf("[mcp] disabled")
		}

		// Discover MCP server at startup via peer registry (already loaded).
		go func() {
			if host, port, ok := sdkClient.GetPeer(mcpPlugin); ok {
				enableMCP(host, port)
				return
			}
			log.Printf("[mcp] %s not in peer registry at startup, waiting for event", mcpPlugin)
		}()

		// Hot-reload when MCP server comes online.
		sdkClient.Events().On("mcp_server:enabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			var detail struct {
				Endpoint string `json:"endpoint"`
			}
			if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
				log.Printf("[mcp] failed to parse mcp_server:enabled detail: %v", err)
				return
			}
			if detail.Endpoint == "" {
				log.Printf("[mcp] mcp_server:enabled with empty endpoint, ignoring")
				return
			}
			u, err := url.Parse(detail.Endpoint)
			if err != nil {
				log.Printf("[mcp] bad endpoint URL %q: %v", detail.Endpoint, err)
				return
			}
			host := u.Hostname()
			port := 8081
			if p := u.Port(); p != "" {
				if pv, err := strconv.Atoi(p); err == nil {
					port = pv
				}
			}
			enableMCP(host, port)
		}))

		sdkClient.Events().On("mcp_server:disabled", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			disableMCP()
		}))

		// When MCP tools change, cycle the CLI process pool so new processes
		// pick up the updated tool list on their next initialize handshake.
		events.OnMCPToolsChanged(sdkClient, func() {
			log.Printf("[mcp] tools changed — cycling CLI process pool")
			adapter.CyclePool()
		})
	}

	// --- Workspace environment mode ---
	// When running as a workspace environment, start the ExecServer on :9100
	// and register with workspace-manager so it can spawn workspace containers.
	if backend == "cli" && execMode != "remote" {
		skipPerms := pluginConfig["CLAUDE_SKIP_PERMISSIONS"] == "true"
		wsCliClient := claudecli.NewClient(cliBinary, workspaceDir, configOrDefault(pluginConfig, "CLAUDE_CONFIG_DIR", "/home/coder/.claude"), cliTimeout, debug)
		wsCliClient.SetPoolMax(1)
		if skipPerms {
			wsCliClient.SetSkipPermissions(true)
		}

		execSrv := exec.NewServer(wsCliClient)
		go execSrv.Start(":9100")

		registerWorkspaceEnv := func() {
			skipVal := "false"
			if skipPerms {
				skipVal = "true"
			}
			payload := events.WorkspaceEnvironmentRegisterPayload{
				PluginID:    manifest.ID,
				DisplayName: "Claude Terminal",
				Description: "Web terminal with Claude Code CLI — AI-powered coding assistant",
				Image:       "teamagentica-devbox-terminal:latest",
				Port:        7681,
				Icon:        `<svg viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="10" fill="#D97706"/><path d="M8 10c0-1.1.9-2 2-2h4c1.1 0 2 .9 2 2v2c0 1.1-.9 2-2 2h-4c-1.1 0-2-.9-2-2v-2z" fill="#fff"/><rect x="9" y="15" width="6" height="2" rx="1" fill="#fff"/></svg>`,
				Disks: []events.WorkspaceDiskSpec{
					{Type: "workspace", Target: "/workspace"},
					{Type: "shared", Name: "agent-anthropic", Target: "/home/coder/.claude"},
					{Type: "shared", Name: "agent-anthropic-sidecar", Target: "/opt/agent-sidecar"},
				},
				EnvDefaults: map[string]string{
					"DEVBOX_APP":              "claude",
					"DEFAULT_WORKSPACE":       "/workspace",
					"HOME":                    "/home/coder",
					"CLAUDE_SKIP_PERMISSIONS": skipVal,
					"TACLI_KERNEL":            "http://teamagentica-kernel:8080",
				},
			}
			b, _ := json.Marshal(payload)
			sdkClient.PublishEvent("workspace:environment:register", string(b))
			log.Printf("[workspace-env] registered workspace environment: %s", manifest.ID)
		}

		sdkClient.Events().On("workspace:manager:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
			registerWorkspaceEnv()
		}))

		registerWorkspaceEnv()
	}

	// Copy sidecar binary to shared disk so workspace containers can run it.
	if _, err := os.Stat("/sidecar-bin"); err == nil {
		if src, err := os.ReadFile("/usr/local/bin/claude-exec-server"); err == nil {
			dst := "/sidecar-bin/claude-exec-server"
			tmp := dst + ".tmp"
			if err := os.WriteFile(tmp, src, 0755); err != nil {
				log.Printf("WARNING: failed to write sidecar binary temp: %v", err)
			} else if err := os.Rename(tmp, dst); err != nil {
				log.Printf("WARNING: failed to rename sidecar binary: %v", err)
				os.Remove(tmp)
			} else {
				log.Printf("[sidecar] wrote exec-server binary to %s", dst)
			}
		}
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
