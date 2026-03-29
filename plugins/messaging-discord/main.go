package main

import (
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

	"github.com/redis/go-redis/v9"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/channels"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/relay"
)

// botEntry is one entry from the BOTS config (type: bot_token).
type botEntry struct {
	Alias string `json:"alias"`
	Token string `json:"token"`
}

// parseBots parses the JSON bot_token array from the BOTS config field.
func parseBots(raw string) []botEntry {
	var entries []botEntry
	if raw == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(raw), &entries)
	// filter out incomplete entries
	var valid []botEntry
	for _, e := range entries {
		if e.Alias != "" && e.Token != "" {
			valid = append(valid, e)
		}
	}
	return valid
}

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
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
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

	debug := pluginConfig["PLUGIN_DEBUG"] == "true" || pluginConfig["PLUGIN_DEBUG"] == "1"

	// Seed aliases from kernel (will update dynamically via alias:update events).
	aliasEntries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(aliasEntries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(aliasEntries))
	}
	aliases := alias.NewAliasMap(aliasEntries)

	// Build the kernel base URL, respecting TLS setting.
	scheme := "http"
	if sdkCfg.TLSCert != "" {
		scheme = "https"
	}
	kernelBaseURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)
	kernelClient := kernel.NewClient(kernelBaseURL, "", sdkClient.TLSConfig())

	guildID := pluginConfig["DISCORD_GUILD_ID"]
	msgBufferMS := 0
	if v := pluginConfig["MESSAGE_BUFFER_MS"]; v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			msgBufferMS = ms
		}
	}

	// --- Bot startup from BOTS config (type: bot_token) ---
	botEntries := parseBots(pluginConfig["BOTS"])

	var bots []*bot.Bot
	if len(botEntries) == 0 {
		log.Printf("No bot identities configured — waiting for BOTS config")
	} else {
		log.Printf("Starting %d bot(s)", len(botEntries))
	}
	for _, entry := range botEntries {
		sourceID := manifest.ID
		if len(botEntries) > 1 {
			sourceID = manifest.ID + ":" + entry.Alias
		}
		b, err := bot.New(entry.Token, kernelClient, aliases)
		if err != nil {
			log.Printf("ERROR: failed to create bot for alias %q: %v (skipping)", entry.Alias, err)
			continue
		}
		b.SetVersion(pluginsdk.DevVersion(manifest.Version))
		b.SetSDK(sdkClient)
		b.SetRelayClient(relay.NewClient(sdkClient, sourceID))
		if guildID != "" {
			b.SetGuildID(guildID)
		}
		if debug {
			b.SetDebug(true)
		}
		if msgBufferMS > 0 {
			b.SetMessageBufferMS(msgBufferMS)
		}
		if err := b.Start(); err != nil {
			log.Printf("ERROR: failed to start bot for alias %q: %v (skipping)", entry.Alias, err)
			continue
		}
		emitCoordinatorEvent(sdkClient, sourceID, entry.Alias)
		bots = append(bots, b)
		log.Printf("Bot started for alias @%s (sourceID=%s)", entry.Alias, sourceID)
	}

	// Primary bot is the first one — used for channel management and slash commands.
	var discordBot *bot.Bot
	if len(bots) > 0 {
		discordBot = bots[0]
	}

	// Acquire cache for welcome message throttling.
	sdkClient.CacheClient(func(c *redis.Client) {
		log.Printf("Cache connected")
		for _, b := range bots {
			b.SetCache(c)
		}
	})

	// Handler for alias registry events (update + ready).
	handleAliasEvent := func(event pluginsdk.EventCallback) {
		infos := convertRegistryAliases(event.Detail)
		if infos == nil {
			log.Printf("Failed to parse alias registry event detail")
			return
		}
		aliases.Replace(infos)
		log.Printf("Hot-swapped %d aliases from registry (seq=%d)", len(infos), event.Seq)
	}

	// Subscribe to alias updates from infra-alias-registry.
	sdkClient.OnEvent("alias-registry:update", pluginsdk.NewTimedDebouncer(2*time.Second, handleAliasEvent))
	sdkClient.OnEvent("alias-registry:ready", pluginsdk.NewTimedDebouncer(1*time.Second, handleAliasEvent))

	// Subscribe to soft config updates for dynamic changes (immediate).
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse config:update detail: %v", err)
			return
		}
		dbg := detail.Config["PLUGIN_DEBUG"] == "true" || detail.Config["PLUGIN_DEBUG"] == "1"
		for _, b := range bots {
			b.SetDebug(dbg)
		}
		if v, ok := detail.Config["MESSAGE_BUFFER_MS"]; ok {
			if ms, err := strconv.Atoi(v); err == nil {
				for _, b := range bots {
					b.SetMessageBufferMS(ms)
				}
			}
		}
	}))

	// Re-discover slash commands when any plugin registers (debounced to coalesce startup bursts).
	sdkClient.OnEvent("plugin:registered", pluginsdk.NewTimedDebouncer(3*time.Second, func(event pluginsdk.EventCallback) {
		log.Printf("plugin:registered — refreshing slash commands")
		for _, b := range bots {
			b.RefreshCommands()
		}
	}))

	// Re-emit coordinator when the relay (re)starts — addressed events are consumed once.
	sdkClient.OnEvent("relay:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		log.Printf("Relay ready — re-emitting coordinator assignments")
		for _, entry := range botEntries {
			sourceID := manifest.ID
			if len(botEntries) > 1 {
				sourceID = manifest.ID + ":" + entry.Alias
			}
			emitCoordinatorEvent(sdkClient, sourceID, entry.Alias)
		}
	}))

	// Handle relay progress events — update Discord messages with task status.
	sdkClient.OnEvent("relay:progress", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		for _, b := range bots {
			b.HandleRelayProgress(event.Detail)
		}
	}))

	// HTTP server for config options, health, and tool endpoints.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /config/options/{field}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"options":[]}`))
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	// Channel management and slash commands require a running bot.
	if discordBot != nil {
		callbackStore := channels.NewCallbackStore()
		discordBot.SetCallbackStore(callbackStore)

		chHandler := channels.NewHandler(discordBot.Session, discordBot.GuildID, callbackStore)

		mux.HandleFunc("GET /discord-commands", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			cmds := discordBot.ListRegisteredCommands()
			if cmds == nil {
				cmds = []bot.RegisteredCommand{}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"commands": cmds})
		})

		mux.HandleFunc("GET /mcp", chHandler.Tools)
		mux.HandleFunc("POST /channels/create-category", chHandler.CreateCategory)
		mux.HandleFunc("POST /channels/create", chHandler.CreateChannel)
		mux.HandleFunc("POST /channels/list", chHandler.ListChannels)
		mux.HandleFunc("POST /channels/delete", chHandler.DeleteChannel)
		mux.HandleFunc("POST /channels/send-menu", chHandler.SendMenu)
	}

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

	// Deregister from kernel first, then close all Discord sessions.
	sdkClient.Stop()
	cancel()

	for _, b := range bots {
		if err := b.Stop(); err != nil {
			log.Printf("Error stopping bot: %v", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	log.Println("Discord plugin shut down")
}

// convertRegistryAliases converts the alias registry event detail into []alias.AliasInfo.
// Registry shape: {"aliases": [{name, type, plugin, provider, model, system_prompt, ...}]}
func convertRegistryAliases(detail string) []alias.AliasInfo {
	var payload struct {
		Aliases []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Plugin string `json:"plugin"`
			Model  string `json:"model"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return nil
	}
	infos := make([]alias.AliasInfo, 0, len(payload.Aliases))
	for _, e := range payload.Aliases {
		target := e.Plugin
		if e.Model != "" {
			target = e.Plugin + ":" + e.Model
		}
		caps := []string{"tool:mcp"}
		if e.Type == "agent" {
			caps = []string{"agent:chat", "tool:mcp"}
		}
		infos = append(infos, alias.AliasInfo{
			Name:         e.Name,
			Target:       target,
			Capabilities: caps,
		})
	}
	return infos
}

// emitCoordinatorEvent tells the relay which alias should coordinate conversations for this plugin.
// Uses addressed delivery so the event queues in the kernel until infra-agent-relay is ready.
func emitCoordinatorEvent(sdk *pluginsdk.Client, sourcePlugin, aliasName string) {
	detail, _ := json.Marshal(map[string]string{
		"source_plugin": sourcePlugin,
		"alias":         aliasName,
	})
	events.PublishRelayCoordinator(sdk, string(detail))
	log.Printf("Emitted relay:coordinator: %s → @%s", sourcePlugin, aliasName)
}
