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

	// Handler reference for SchemaFunc/ToolsFunc closures (set after handler creation).
	var handlerRef *handlers.Handler

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
			if handlerRef != nil {
				schema["memory_activity"] = handlerRef.ActivityEntries()
			}
			return schema
		},
		ToolsFunc: func() interface{} {
			if handlerRef != nil {
				return handlerRef.ToolDefs()
			}
			return nil
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

	extractionPersona := "brains"
	if v := pluginConfig["EXTRACTION_PERSONA"]; v != "" {
		extractionPersona = v
	}

	compactMsgThreshold := 100
	if v := pluginConfig["COMPACT_AFTER_MESSAGES"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			compactMsgThreshold = n
		}
	}

	compactInterval := 30 * time.Minute
	if v := pluginConfig["COMPACT_INTERVAL_MINUTES"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			compactInterval = time.Duration(n) * time.Minute
		}
	}

	activityLogSize := 100
	if v := pluginConfig["ACTIVITY_LOG_SIZE"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			activityLogSize = n
		}
	}

	router := gin.Default()
	h := handlers.New(db, maxMsgPerSession)
	h.SetSDK(sdkClient)
	h.SetExtractionPersona(extractionPersona)
	h.SetActivityLog(handlers.NewActivityLog(activityLogSize))
	handlerRef = h // Enable SchemaFunc to access activity log.

	// Start the compaction buffer.
	compactor := handlers.NewCompactor(h, compactMsgThreshold, compactInterval)
	h.SetCompactor(compactor)
	compactor.Start()

	// Health
	router.GET("/health", h.Health)

	// Session REST API (used by relay and admin tooling)
	router.GET("/sessions", h.ListSessions)
	router.GET("/sessions/:id/messages", h.GetHistory)
	router.POST("/sessions/:id/messages", h.AddMessage)
	router.DELETE("/sessions/:id", h.ClearSession)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_sessions", h.MCPListSessions)
	router.POST("/mcp/get_history", h.MCPGetHistory)
	router.POST("/mcp/add_message", h.MCPAddMessage)
	router.POST("/mcp/clear_session", h.MCPClearSession)

	// Memory facts MCP tools
	router.POST("/mcp/recall_memory", h.MCPRecallMemory)
	router.POST("/mcp/save_memory", h.MCPSaveMemory)
	router.POST("/mcp/update_memory", h.MCPUpdateMemory)
	router.POST("/mcp/delete_memory", h.MCPDeleteMemory)
	router.POST("/mcp/list_memories", h.MCPListMemories)

	// Compact (fact extraction)
	router.POST("/mcp/compact_conversation", h.MCPCompact)
	router.POST("/compact", h.MCPCompact)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
