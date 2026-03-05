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
	"github.com/antimatter-studios/teamagentica/plugins/chat/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/chat/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/chat/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/chat/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	db, err := storage.Open(cfg.DataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	files, err := storage.NewFileStore(cfg.DataPath)
	if err != nil {
		log.Fatalf("failed to init file store: %v", err)
	}

	// SDK config for kernel connection.
	sdkCfg := pluginsdk.LoadConfig()

	// Seed aliases from kernel.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"system:chat"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"DEFAULT_AGENT": {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator. Leave empty to require manual agent selection.", Order: 1},
			"PLUGIN_DEBUG":  {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
		},
	})

	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	// Kernel client for agent chat.
	kernelBaseURL := fmt.Sprintf("http://%s:%s", sdkCfg.KernelHost, sdkCfg.KernelPort)
	kc := kernel.NewClient(kernelBaseURL, sdkCfg.PluginToken, cfg.Debug)

	h := handlers.NewHandler(db, files, kc, sdkClient, aliases, cfg.DefaultAgent, cfg.Debug)

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

	// Subscribe to config updates for DEFAULT_AGENT.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			return
		}
		if agent, ok := detail.Config["DEFAULT_AGENT"]; ok {
			cfg.DefaultAgent = agent
			h.SetDefaultAgent(agent)
			log.Printf("DEFAULT_AGENT updated to %q", agent)
		}
	}))

	sdkClient.Start(context.Background())

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
