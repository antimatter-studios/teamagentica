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

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/kernel"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	// Determine hostname and plugin ID for registration.
	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "messaging-discord"
	}

	const httpPort = 8092

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: []string{"messaging:discord", "messaging:send", "messaging:receive"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"DISCORD_BOT_TOKEN": {Type: "string", Label: "Bot Token", Required: true, Secret: true, HelpText: "Discord bot token from developer portal", Order: 1},
			"DEFAULT_AGENT":     {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator", Order: 5},
			"MESSAGE_BUFFER_MS": {Type: "number", Label: "Message Buffer (ms)", Default: "1000", HelpText: "Debounce window for consolidating sequential messages (e.g. forwarded image + text). Set to 0 to disable.", Order: 6},
			"PLUGIN_DEBUG":      {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic to the debug console (may include sensitive data)", Order: 99},
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
	if debug {
		discordBot.SetDebug(true)
	}
	if agent := pluginConfig["DEFAULT_AGENT"]; agent != "" {
		discordBot.SetDefaultAgent(agent)
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
		if agent, ok := detail.Config["DEFAULT_AGENT"]; ok {
			discordBot.SetDefaultAgent(agent)
		}
		if d, ok := detail.Config["PLUGIN_DEBUG"]; ok {
			discordBot.SetDebug(d == "true" || d == "1")
		}
		if v, ok := detail.Config["MESSAGE_BUFFER_MS"]; ok {
			if ms, err := strconv.Atoi(v); err == nil {
				discordBot.SetMessageBufferMS(ms)
			}
		}
	}))

	if err := discordBot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	// HTTP server for config options and health.
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
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

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
