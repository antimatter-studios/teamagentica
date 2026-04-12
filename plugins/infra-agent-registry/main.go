package main

import (
	"context"
	_ "embed"
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-registry/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-registry/internal/storage"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	h := handlers.New(nil, defaultSystemPrompt)

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ToolsFunc: func() interface{} {
			return h.ToolDefs()
		},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	dataPath := "/data"

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	router := gin.Default()
	h = handlers.New(db, defaultSystemPrompt)
	h.SetSDK(sdkClient)

	// Health
	router.GET("/health", h.Health)

	// Agent REST API
	router.GET("/agents", h.ListAgents)
	router.GET("/agents/default", h.GetDefaultAgent)
	router.GET("/agents/by-type/:type", h.ListAgentsByType)
	router.GET("/agents/:alias", h.GetAgent)
	router.POST("/agents", h.CreateAgent)
	router.PUT("/agents/:alias", h.UpdateAgent)
	router.POST("/agents/:alias/set-default", h.SetDefaultAgent)
	router.DELETE("/agents/:alias", h.DeleteAgent)

	// Alias REST API
	router.GET("/aliases", h.ListAliases)
	router.GET("/alias/:name", h.GetAlias)
	router.POST("/aliases", h.CreateAlias)
	router.PUT("/aliases/:name", h.UpdateAlias)
	router.DELETE("/aliases/:name", h.DeleteAlias)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_agents", h.MCPListAgents)
	router.POST("/mcp/get_agent", h.MCPGetAgent)
	router.POST("/mcp/create_agent", h.MCPCreateAgent)
	router.POST("/mcp/update_agent", h.MCPUpdateAgent)
	router.POST("/mcp/delete_agent", h.MCPDeleteAgent)
	router.POST("/mcp/get_default_agent", h.MCPGetDefaultAgent)
	router.POST("/mcp/set_default_agent", h.MCPSetDefaultAgent)

	// Notify subscribers (relay, messaging plugins) that agents are ready.
	h.BroadcastReady()

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(defaultPort, router)
}
