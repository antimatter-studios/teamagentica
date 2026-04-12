package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/relay"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname := getHostname()
	manifest := pluginsdk.LoadManifest()

	const httpPort = 8092

	var h *handlers.Handler

	dataPath := "/data" // default, overridden by FetchConfig below

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
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
		ToolsFunc: func() interface{} {
			if h != nil {
				return h.ToolDefs()
			}
			return nil
		},
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

	rc := relay.NewClient(sdkClient, manifest.ID)

	h = handlers.NewHandler(db, files, rc, sdkClient, aliases, debug)

	router := gin.Default()
	router.GET("/health", h.Health)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/agents", h.ListAgents)
	router.GET("/conversations", h.ListConversations)
	router.POST("/conversations", h.CreateConversation)
	router.GET("/conversations/:id", h.GetConversation)
	router.PUT("/conversations/:id", h.UpdateConversation)
	router.DELETE("/conversations/:id", h.DeleteConversation)
	router.POST("/conversations/:id/read", h.MarkRead)
	router.POST("/conversations/:id/messages", h.SendMessage)
	router.POST("/upload", h.Upload)
	router.GET("/files/*filepath", h.ServeFile)

	// MCP tool discovery + endpoints.
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/list_conversations", h.ToolListConversations)
	router.POST("/mcp/get_messages", h.ToolGetMessages)
	router.POST("/mcp/post_message", h.ToolPostMessage)
	router.POST("/mcp/create_conversation", h.ToolCreateConversation)

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

	// Subscribe to alias updates from infra-agent-persona.
	sdkClient.Events().On("agent:update", pluginsdk.NewTimedDebouncer(2*time.Second, handleAliasEvent))
	sdkClient.Events().On("agent:ready", pluginsdk.NewTimedDebouncer(1*time.Second, handleAliasEvent))

	// Handle progress updates from the relay (thinking, running, completed, failed).
	sdkClient.Events().On("relay:progress", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		h.HandleRelayProgress(event.Detail)
	}))

	// Subscribe to config updates for PLUGIN_DEBUG.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		// Future: handle PLUGIN_DEBUG toggle here.
		_ = p
	})

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(httpPort, router)
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
		var caps []string
		switch e.Type {
		case "agent":
			caps = []string{"agent:chat"}
		case "tool_agent":
			caps = []string{"agent:tool"}
		default:
			caps = []string{"tool:mcp"}
		}
		infos = append(infos, alias.AliasInfo{
			Name:         e.Name,
			Target:       target,
			Capabilities: caps,
		})
	}
	return infos
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
