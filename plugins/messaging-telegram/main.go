package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/relay"
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
	hostname := getHostname()
	manifest := pluginsdk.LoadManifest()

	const httpPort = 8443

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

	// Seed aliases from kernel (will update dynamically via alias:update events).
	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	// Root context for the entire process.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Start SDK (register with kernel + heartbeat loop + event server + subscriptions).
	sdkClient.Start(rootCtx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true" || pluginConfig["PLUGIN_DEBUG"] == "1"

	pollTimeout := 60
	if v := pluginConfig["TELEGRAM_POLL_TIMEOUT"]; v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			pollTimeout = parsed
		}
	}

	allowedUsers := parseAllowedUsers(pluginConfig["TELEGRAM_ALLOWED_USERS"])

	// Build the kernel base URL, respecting TLS setting.
	scheme := "http"
	if sdkCfg.TLSCert != "" {
		scheme = "https"
	}
	kernelBaseURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)

	// Persistent data directory for bot state (known groups, etc.).
	dataDir := "/data"
	os.MkdirAll(dataDir, 0755)

	kernelClient := kernel.NewClient(kernelBaseURL, "", debug)

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
		b, err := bot.New(rootCtx, entry.Token, kernelClient, sourceID,
			allowedUsers, pollTimeout, debug, aliases, dataDir)
		if err != nil {
			log.Printf("ERROR: failed to create bot for alias %q: %v (skipping)", entry.Alias, err)
			continue
		}
		b.SetVersion(pluginsdk.DevVersion(manifest.Version))
		b.SetRelayClient(relay.NewClient(sdkClient, sourceID))
		if msgBufferMS > 0 {
			b.SetMessageBufferMS(msgBufferMS)
		}
		emitCoordinatorEvent(sdkClient, sourceID, entry.Alias)
		bots = append(bots, b)
		log.Printf("Bot started for alias @%s (sourceID=%s)", entry.Alias, sourceID)
	}

	// Primary bot — used for webhook mode and config updates.
	var telegramBot *bot.Bot
	if len(bots) > 0 {
		telegramBot = bots[0]
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

	// Subscribe to soft config updates (immediate).
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse config:update detail: %v", err)
			return
		}
		if v, ok := detail.Config["MESSAGE_BUFFER_MS"]; ok {
			if ms, err := strconv.Atoi(v); err == nil {
				for _, b := range bots {
					b.SetMessageBufferMS(ms)
				}
			}
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

	// Register webhook route with the ingress (handles webhook:ready subscription).
	sdkClient.RegisterWebhook("/" + manifest.ID)

	// When we receive our public webhook URL, switch from polling to webhook mode.
	sdkClient.OnWebhookURL(func(webhookURL string) {
		if telegramBot == nil {
			log.Printf("webhook URL received but no bots configured")
			return
		}

		webhookURL = strings.TrimRight(webhookURL, "/") + "/webhook"
		log.Printf("Received webhook URL: %s", webhookURL)

		// Skip if already in webhook mode.
		if telegramBot.IsWebhookActive() {
			log.Printf("Already in webhook mode, ignoring duplicate URL")
			return
		}

		// Stop polling, switch to webhook.
		events.PublishPollStop(sdkClient, "stopping polling — webhook URL received")
		telegramBot.StopPolling()

		events.PublishStatus(sdkClient, "webhook", fmt.Sprintf("setting Telegram webhook url=%s", webhookURL))
		if err := telegramBot.SetWebhook(webhookURL); err != nil {
			log.Printf("Failed to set webhook: %v — falling back to polling", err)
			events.PublishWebhookError(sdkClient, "", "", fmt.Sprintf("setWebhook failed: %v — falling back to polling", err))
			events.PublishPollStart(sdkClient, "mode=polling (webhook fallback)")
			telegramBot.StartPolling()
			return
		}

		events.PublishStatus(sdkClient, "webhook", fmt.Sprintf("mode=webhook active url=%s", webhookURL))
	})

	// Handle relay progress events — update Telegram messages with task status.
	sdkClient.OnEvent("relay:progress", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		if telegramBot != nil {
			telegramBot.HandleRelayProgress(event.Detail)
		}
	}))

	// Start polling on all bots. For multi-bot mode, webhook is not supported
	// (webhook requires a single URL; each bot would need its own URL).
	events.PublishPollStart(sdkClient, "mode=polling (default)")
	for _, b := range bots {
		b.StartPolling()
	}

	// Set up HTTP routes.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /schema", sdkClient.SchemaHandler())
	mux.HandleFunc("POST /events", sdkClient.EventHandler())

	mux.HandleFunc("GET /config/options/{field}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"options":[]}`))
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mode := "idle"
		if telegramBot != nil {
			mode = "polling"
			if telegramBot.IsWebhookActive() {
				mode = "webhook"
			}
		}
		fmt.Fprintf(w, `{"status":"ok","mode":%q}`, mode)
	})

	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		if telegramBot == nil {
			http.Error(w, "no bots configured", http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading webhook body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if debug {
			log.Printf("[webhook] received %d bytes from %s", len(body), r.RemoteAddr)
			log.Printf("[webhook] body: %s", string(body))
		} else {
			log.Printf("[webhook] received %d bytes", len(body))
		}

		if err := telegramBot.HandleWebhookUpdate(body); err != nil {
			log.Printf("Error processing webhook update: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	})

	// Configure HTTP server with TLS if enabled.
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}

	tlsConfig, err := pluginsdk.GetServerTLSConfig(sdkCfg)
	if err != nil {
		log.Fatalf("Failed to configure server TLS: %v", err)
	}

	// Start HTTP server in a goroutine.
	go func() {
		if tlsConfig != nil {
			server.TLSConfig = tlsConfig
			log.Printf("HTTP server listening on %s (mTLS enabled)", server.Addr)
			if err := server.ListenAndServeTLS(sdkCfg.TLSCert, sdkCfg.TLSKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error: %v", err)
			}
		} else {
			log.Printf("HTTP server listening on %s", server.Addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error: %v", err)
			}
		}
	}()

	// Wait for shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal %s, shutting down...", sig)

	// Shutdown order:
	// 1. Stop all bots (cancel ctx, drain goroutines) — stops long-poll immediately.
	for _, b := range bots {
		b.Stop()
	}

	// 2. Deregister from kernel.
	sdkClient.Stop()

	// 3. Cancel root context.
	rootCancel()

	// 4. Gracefully shut down HTTP server.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Telegram plugin shut down")
}

// parseAllowedUsers parses a comma-separated list of Telegram user IDs into a set.
// Returns nil if the list is empty (all users allowed).
func parseAllowedUsers(raw string) map[int64]bool {
	if raw == "" {
		return nil
	}

	allowed := make(map[int64]bool)
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		allowed[id] = true
	}

	if len(allowed) == 0 {
		return nil
	}
	return allowed
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

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
