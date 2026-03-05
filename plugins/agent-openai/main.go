package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	// Set up Gin router.
	router := gin.Default()

	// Create handler with config.
	h := handlers.NewHandler(cfg)

	// Initialise the appropriate backend.
	if cfg.Backend == "subscription" {
		log.Println("[subscription] initialising Codex CLI backend")
		workdir := cfg.CodexDataPath + "/codex-workspace"
		codexHome := cfg.CodexDataPath + "/codex-home"
		for _, dir := range []string{workdir, codexHome} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Fatalf("failed to create directory %s: %v", dir, err)
			}
		}

		cliClient := codexcli.NewClient(cfg.CodexCLIBinary, workdir, codexHome, cfg.CodexCLITimeout, cfg.Debug)
		h.SetCodexCLI(cliClient)

		if cliClient.IsAuthenticated() {
			log.Println("[subscription] Codex CLI is authenticated")
		} else {
			log.Println("[subscription] WARNING: Codex CLI is NOT authenticated — run 'codex login --device-auth' in the container")
		}
	}

	// Register routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
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

	// Create plugin SDK client and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"ai:chat", "ai:chat:openai"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"OPENAI_BACKEND": {Type: "select", Label: "Backend", Default: "subscription", Options: []string{"subscription", "api_key"}, HelpText: "Choose how to authenticate with OpenAI", Order: 1},
			"OPENAI_AUTH":    {Type: "oauth", Label: "Login with OpenAI", HelpText: "Authenticate with your OpenAI account to use Codex models", VisibleWhen: &pluginsdk.VisibleWhen{Field: "OPENAI_BACKEND", Value: "subscription"}, Order: 2},
			"OPENAI_API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true, HelpText: "Get your API key at https://platform.openai.com/api-keys", VisibleWhen: &pluginsdk.VisibleWhen{Field: "OPENAI_BACKEND", Value: "api_key"}, Order: 2},
			"OPENAI_MODEL":   {Type: "select", Label: "Model", Default: "gpt-4o", Dynamic: true, Order: 3},
			"PLUGIN_ALIASES": {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin. Each alias maps a short name to a plugin:model target.", Order: 90},
			"PLUGIN_DEBUG":   {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
		},
	})

	// Subscribe to MCP server events (must be before Start).
	if cfg.Backend == "subscription" {
		codexHome := cfg.CodexDataPath + "/codex-home"

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

	sdkClient.Start(context.Background())
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
