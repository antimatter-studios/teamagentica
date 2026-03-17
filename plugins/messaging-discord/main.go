package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/channels"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/relay"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	// Determine hostname and plugin ID for registration.
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const httpPort = 8092

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ConfigSchema: manifest.ConfigSchema,
	})

	// Start SDK first (register with kernel + heartbeat loop + event server).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	discordToken := pluginConfig["DISCORD_BOT_TOKEN"]
	if discordToken == "" {
		log.Fatalf("DISCORD_BOT_TOKEN not configured — set it in the plugin settings")
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true" || pluginConfig["PLUGIN_DEBUG"] == "1"

	// Seed aliases from kernel (will update dynamically via alias:update events).
	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	// Build the kernel base URL, respecting TLS setting.
	scheme := "http"
	if sdkCfg.TLSEnabled {
		scheme = "https"
	}
	kernelBaseURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)

	// Create and start the Discord bot.
	kernelClient := kernel.NewClient(kernelBaseURL, sdkCfg.PluginToken, sdkClient.TLSConfig())

	discordBot, err := bot.New(discordToken, kernelClient, aliases)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	discordBot.SetSDK(sdkClient)
	discordBot.SetRelayClient(relay.NewClient(sdkClient, manifest.ID))

	if guildID := pluginConfig["DISCORD_GUILD_ID"]; guildID != "" {
		discordBot.SetGuildID(guildID)
	}

	// Set coordinator on relay if configured.
	if coordAlias := pluginConfig["COORDINATOR_ALIAS"]; coordAlias != "" {
		setCoordinatorOnRelay(sdkClient, manifest.ID, coordAlias)
	}
	if debug {
		discordBot.SetDebug(true)
	}
	if v := pluginConfig["MESSAGE_BUFFER_MS"]; v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			discordBot.SetMessageBufferMS(ms)
		}
	}

	// Subscribe to live alias updates from kernel (debounced — coalesce rapid updates).
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse kernel:alias:update detail: %v", err)
			return
		}
		aliases.Replace(detail.Aliases)
		log.Printf("Hot-swapped %d aliases (seq=%d)", len(detail.Aliases), event.Seq)
	}))

	// Subscribe to soft config updates for dynamic changes (immediate).
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse config:update detail: %v", err)
			return
		}
		if d, ok := detail.Config["PLUGIN_DEBUG"]; ok {
			discordBot.SetDebug(d == "true" || d == "1")
		}
		if v, ok := detail.Config["MESSAGE_BUFFER_MS"]; ok {
			if ms, err := strconv.Atoi(v); err == nil {
				discordBot.SetMessageBufferMS(ms)
			}
		}
		if v, ok := detail.Config["COORDINATOR_ALIAS"]; ok {
			setCoordinatorOnRelay(sdkClient, manifest.ID, v)
		}
	}))

	// Re-discover slash commands when any plugin registers (debounced to coalesce startup bursts).
	sdkClient.OnEvent("plugin:registered", pluginsdk.NewTimedDebouncer(3*time.Second, func(event pluginsdk.EventCallback) {
		log.Printf("plugin:registered — refreshing slash commands")
		discordBot.RefreshCommands()
	}))

	// Re-send coordinator when the relay (re)starts — it stores the mapping in memory.
	sdkClient.OnEvent("relay:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		if coordAlias := pluginConfig["COORDINATOR_ALIAS"]; coordAlias != "" {
			log.Printf("Relay ready — re-sending coordinator assignment")
			setCoordinatorOnRelay(sdkClient, manifest.ID, coordAlias)
		}
	}))

	if err := discordBot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	// Shared callback store for interactive menus.
	callbackStore := channels.NewCallbackStore()
	discordBot.SetCallbackStore(callbackStore)

	// Channel management tool handler (for MCP agent discovery).
	chHandler := channels.NewHandler(discordBot.Session, discordBot.GuildID, callbackStore)

	// HTTP server for config options, health, and tool endpoints.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /config/options/{field}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		field := r.PathValue("field")
		if field == "COORDINATOR_ALIAS" {
			agentAliases := aliases.ListAgentAliases()
			json.NewEncoder(w).Encode(map[string][]string{"options": agentAliases})
			return
		}
		w.Write([]byte(`{"options":[]}`))
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("GET /discord-commands", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cmds := discordBot.ListRegisteredCommands()
		if cmds == nil {
			cmds = []bot.RegisteredCommand{}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"commands": cmds})
	})

	// Tool endpoints for MCP agent discovery.
	mux.HandleFunc("GET /tools", chHandler.Tools)
	mux.HandleFunc("POST /channels/create-category", chHandler.CreateCategory)
	mux.HandleFunc("POST /channels/create", chHandler.CreateChannel)
	mux.HandleFunc("POST /channels/list", chHandler.ListChannels)
	mux.HandleFunc("POST /channels/delete", chHandler.DeleteChannel)
	mux.HandleFunc("POST /channels/send-menu", chHandler.SendMenu)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}

	go func() {
		log.Printf("HTTP server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for shutdown signal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	// Deregister from kernel first, then close Discord session.
	sdkClient.Stop()
	cancel()

	if err := discordBot.Stop(); err != nil {
		log.Printf("Error stopping bot: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	log.Println("Discord plugin shut down")
}

// setCoordinatorOnRelay tells the relay which alias should coordinate conversations for this plugin.
func setCoordinatorOnRelay(sdk *pluginsdk.Client, sourcePlugin, aliasName string) {
	payload, _ := json.Marshal(map[string]string{
		"source_plugin": sourcePlugin,
		"alias":         aliasName,
	})
	_, err := sdk.RouteToPlugin(context.Background(), "infra-agent-relay", "POST", "/config/coordinator", bytes.NewReader(payload))
	if err != nil {
		log.Printf("Failed to set coordinator on relay: %v", err)
		return
	}
	log.Printf("Coordinator set on relay: %s → @%s", sourcePlugin, aliasName)
}
