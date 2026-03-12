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

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/relay"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	// Determine hostname and plugin ID for registration.
	hostname := getHostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "messaging-telegram"
	}

	const httpPort = 8443

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: []string{"messaging:telegram", "messaging:send", "messaging:receive"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"TELEGRAM_BOT_TOKEN":     {Type: "string", Label: "Bot Token", Required: true, Secret: true, HelpText: "Telegram bot token from @BotFather", Order: 1},
			"TELEGRAM_MODE":          {Type: "select", Label: "Update Mode", Default: "poll", Options: []string{"poll", "webhook"}, HelpText: "How the bot receives messages from Telegram", Order: 2},
			"TELEGRAM_POLL_TIMEOUT":  {Type: "number", Label: "Poll Timeout (seconds)", Default: "60", HelpText: "Long poll timeout — Telegram holds the connection open for this many seconds waiting for new messages", VisibleWhen: &pluginsdk.VisibleWhen{Field: "TELEGRAM_MODE", Value: "poll"}, Order: 3},
			"TELEGRAM_WEBHOOK_URL":   {Type: "string", Label: "Webhook URL", HelpText: "Public HTTPS URL that Telegram will POST updates to", VisibleWhen: &pluginsdk.VisibleWhen{Field: "TELEGRAM_MODE", Value: "webhook"}, Order: 3},
			"TELEGRAM_ALLOWED_USERS": {Type: "string", Label: "Allowed User IDs", HelpText: "Comma-separated Telegram user IDs. Leave empty to allow all users.", Order: 4},
			"DEFAULT_AGENT":          {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator. Leave empty to require @mention routing.", Order: 5},
			"MESSAGE_BUFFER_MS":      {Type: "number", Label: "Message Buffer (ms)", Default: "1000", HelpText: "Debounce window for consolidating sequential messages (e.g. forwarded image + text). Set to 0 to disable.", Order: 6},
			"PLUGIN_DEBUG":           {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
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

	telegramToken := pluginConfig["TELEGRAM_BOT_TOKEN"]
	if telegramToken == "" {
		log.Fatalf("TELEGRAM_BOT_TOKEN not configured — set it in the plugin settings")
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
	if sdkCfg.TLSEnabled {
		scheme = "https"
	}
	kernelBaseURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)

	// Create the Telegram bot.
	kernelClient := kernel.NewClient(kernelBaseURL, sdkCfg.PluginToken, debug)
	telegramBot, err := bot.New(rootCtx, telegramToken, kernelClient, pluginID,
		allowedUsers, pollTimeout, debug, aliases)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	telegramBot.SetRelayClient(relay.NewClient(sdkClient, pluginID))

	// Apply initial DEFAULT_AGENT from fetched config.
	if agent := pluginConfig["DEFAULT_AGENT"]; agent != "" {
		telegramBot.SetDefaultAgent(agent)
	}

	// Apply initial MESSAGE_BUFFER_MS from fetched config.
	if v := pluginConfig["MESSAGE_BUFFER_MS"]; v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			telegramBot.SetMessageBufferMS(ms)
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

	// Subscribe to soft config updates for dynamic DEFAULT_AGENT changes (immediate).
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse config:update detail: %v", err)
			return
		}
		if agent, ok := detail.Config["DEFAULT_AGENT"]; ok {
			telegramBot.SetDefaultAgent(agent)
		}
		if v, ok := detail.Config["MESSAGE_BUFFER_MS"]; ok {
			if ms, err := strconv.Atoi(v); err == nil {
				telegramBot.SetMessageBufferMS(ms)
			}
		}
	}))

	// When network-webhook-ingress broadcasts webhook:ready, send our route info.
	sdkClient.OnEvent("webhook:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		log.Printf("Received webhook:ready — sending route update to network-webhook-ingress")
		sendRouteUpdate(sdkClient, pluginID, hostname, httpPort)
	}))

	// When network-webhook-ingress sends us our full webhook URL, switch to webhook mode.
	sdkClient.OnEvent("webhook:plugin:url", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("Failed to parse webhook:plugin:url: %v", err)
			return
		}
		if data.WebhookURL == "" {
			log.Printf("webhook:plugin:url has empty webhook_url")
			return
		}

		webhookURL := strings.TrimRight(data.WebhookURL, "/") + "/webhook"
		log.Printf("Received webhook URL: %s", webhookURL)

		// Skip if already in webhook mode with the same URL.
		if telegramBot.IsWebhookActive() {
			log.Printf("Already in webhook mode, ignoring duplicate webhook:plugin:url")
			return
		}

		// Stop polling, switch to webhook.
		sdkClient.ReportEvent("poll_stop", "stopping polling — webhook URL received")
		telegramBot.StopPolling()

		sdkClient.ReportEvent("webhook", fmt.Sprintf("setting Telegram webhook url=%s", webhookURL))
		if err := telegramBot.SetWebhook(webhookURL); err != nil {
			log.Printf("Failed to set webhook: %v — falling back to polling", err)
			sdkClient.ReportEvent("webhook:error", fmt.Sprintf("setWebhook failed: %v — falling back to polling", err))
			sdkClient.ReportEvent("poll_start", "mode=polling (webhook fallback)")
			telegramBot.StartPolling()
			return
		}

		sdkClient.ReportEvent("webhook", fmt.Sprintf("mode=webhook active url=%s", webhookURL))
	}))

	// Start in polling mode — will be stopped if webhook:plugin:url arrives.
	sdkClient.ReportEvent("poll_start", "mode=polling (default)")
	telegramBot.StartPolling()

	// Set up HTTP routes.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /config/options/{field}", func(w http.ResponseWriter, r *http.Request) {
		field := r.PathValue("field")
		w.Header().Set("Content-Type", "application/json")
		if field == "DEFAULT_AGENT" {
			entries := aliases.List()
			var names []string
			for _, e := range entries {
				if e.Target.Type == alias.TargetAgent {
					names = append(names, e.Alias)
				}
			}
			data, _ := json.Marshal(map[string]interface{}{"options": names})
			w.Write(data)
			return
		}
		w.Write([]byte(`{"options":[]}`))
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mode := "polling"
		if telegramBot.IsWebhookActive() {
			mode = "webhook"
		}
		fmt.Fprintf(w, `{"status":"ok","mode":%q}`, mode)
	})

	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
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
			// Still return 200 to Telegram so it doesn't retry.
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
	// 1. Stop bot (cancel ctx, drain goroutines) — stops long-poll immediately.
	telegramBot.Stop()

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

// sendRouteUpdate sends an addressed webhook:api:update event to network-webhook-ingress.
func sendRouteUpdate(sdkClient *pluginsdk.Client, pluginID, hostname string, port int) {
	payload := map[string]interface{}{
		"plugin_id":   pluginID,
		"prefix":      "/" + pluginID,
		"target_host": hostname,
		"target_port": port,
	}
	data, _ := json.Marshal(payload)
	sdkClient.ReportAddressedEvent("webhook:api:update", string(data), "network-webhook-ingress")
	log.Printf("Sent webhook:api:update to network-webhook-ingress: prefix=/%s target=%s:%d", pluginID, hostname, port)
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

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
