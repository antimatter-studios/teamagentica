package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	if v := pluginConfig["PLUGIN_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	dataPath := pluginConfig["PLUGIN_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}

	maxMsgPerSession := 50
	if v := pluginConfig["MAX_SESSION_MESSAGES"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxMsgPerSession = n
		}
	}

	sessionTTLHours := 24
	if v := pluginConfig["SESSION_TTL_HOURS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sessionTTLHours = n
		}
	}

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	// Prune expired sessions on startup, then every hour.
	go func() {
		prune := func() {
			n, err := db.PruneExpiredSessions(sessionTTLHours)
			if err != nil {
				log.Printf("[memory] prune error: %v", err)
			} else if n > 0 {
				log.Printf("[memory] pruned %d messages from expired sessions", n)
			}
		}
		prune()
		for range time.Tick(time.Hour) {
			prune()
		}
	}()

	router := gin.Default()
	h := handlers.New(db, maxMsgPerSession)

	// Health
	router.GET("/health", h.Health)

	// Session REST API (used by relay and admin tooling)
	router.GET("/sessions", h.ListSessions)
	router.GET("/sessions/:id/messages", h.GetHistory)
	router.POST("/sessions/:id/messages", h.AddMessage)
	router.DELETE("/sessions/:id", h.ClearSession)

	// MCP tool discovery + execution
	router.GET("/tools", h.GetTools)
	router.POST("/mcp/list_sessions", h.MCPListSessions)
	router.POST("/mcp/get_history", h.MCPGetHistory)
	router.POST("/mcp/add_message", h.MCPAddMessage)
	router.POST("/mcp/clear_session", h.MCPClearSession)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
