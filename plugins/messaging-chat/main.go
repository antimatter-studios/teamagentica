package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname := getHostname()
	manifest := pluginsdk.LoadManifest()

	const httpPort = 8092

	// Data path still comes from env — it's infrastructure config, not plugin config.
	dataPath := os.Getenv("PLUGIN_DATA_PATH")
	if dataPath == "" {
		dataPath = "/data"
	}

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	files, err := storage.NewFileStore(dataPath)
	if err != nil {
		log.Fatalf("failed to init file store: %v", err)
	}

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ConfigSchema: manifest.ConfigSchema,
	})

	// Seed aliases from kernel.
	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	// Start SDK (register with kernel + heartbeat loop + event server + subscriptions).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true" || pluginConfig["PLUGIN_DEBUG"] == "1"
	defaultAgent := pluginConfig["DEFAULT_AGENT"]

	// Build the kernel base URL, respecting TLS setting.
	scheme := "http"
	if sdkCfg.TLSEnabled {
		scheme = "https"
	}
	kernelBaseURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)
	kc := kernel.NewClient(kernelBaseURL, sdkCfg.PluginToken, debug)

	h := handlers.NewHandler(db, files, kc, sdkClient, aliases, defaultAgent, debug)

	router := gin.Default()
	router.GET("/health", h.Health)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/agents", h.ListAgents)
	router.GET("/conversations", h.ListConversations)
	router.POST("/conversations", h.CreateConversation)
	router.GET("/conversations/:id", h.GetConversation)
	router.PUT("/conversations/:id", h.UpdateConversation)
	router.DELETE("/conversations/:id", h.DeleteConversation)
	router.POST("/conversations/:id/messages", h.SendMessage)
	router.POST("/upload", h.Upload)
	router.GET("/files/*filepath", h.ServeFile)

	// Subscribe to live alias updates (debounced 2s).
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse kernel:alias:update: %v", err)
			return
		}
		aliases.Replace(detail.Aliases)
		log.Printf("Hot-swapped %d aliases (seq=%d)", len(detail.Aliases), event.Seq)
	}))

	// Subscribe to config updates for DEFAULT_AGENT and PLUGIN_DEBUG.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			return
		}
		if agent, ok := detail.Config["DEFAULT_AGENT"]; ok {
			h.SetDefaultAgent(agent)
			log.Printf("DEFAULT_AGENT updated to %q", agent)
		}
	}))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
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
