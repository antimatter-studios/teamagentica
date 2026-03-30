package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/scheduler"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8081

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	hostname, _ := os.Hostname()

	h := handlers.NewHandler(nil, nil) // pre-create for ToolsFunc; re-assigned after DB init

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
		ToolsFunc: func() interface{} {
			return h.ToolDefs()
		},
	})

	sdkClient.Start(context.Background())

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("Warning: failed to fetch plugin config: %v (using defaults)", err)
		pluginConfig = map[string]string{}
	}

	port := defaultPort
	if v := pluginConfig["SCHEDULER_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	dataPath := "/data"

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Parse dispatch config
	dispatchCfg := scheduler.DispatchConfig{
		Enabled: pluginConfig["DISPATCH_ENABLED"] != "false", // default true
	}
	if v := pluginConfig["DISPATCH_GLOBAL_LIMIT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			dispatchCfg.GlobalLimit = n
		}
	}
	if v := pluginConfig["DISPATCH_AGENT_LIMITS"]; v != "" {
		var limits map[string]int
		if err := json.Unmarshal([]byte(v), &limits); err == nil {
			dispatchCfg.AgentLimits = limits
		}
	}
	if v := pluginConfig["DISPATCH_PROMPT_TEMPLATE"]; v != "" {
		dispatchCfg.PromptTemplate = v
	}

	sched := scheduler.New(db, sdkClient, dispatchCfg)
	defer sched.Stop()

	h = handlers.NewHandler(sched, db)

	router := gin.Default()

	// SDK helper handlers.
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))

	router.GET("/health", h.Health)
	router.POST("/events", h.CreateJob)
	router.GET("/events", h.ListJobs)
	router.GET("/events/:id", h.GetJob)
	router.PUT("/events/:id", h.UpdateJob)
	router.DELETE("/events/:id", h.DeleteJob)
	router.GET("/log", h.GetLog)

	// MCP tool endpoints.
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_jobs", h.MCPListJobs)
	router.POST("/mcp/create_job", h.MCPCreateJob)
	router.POST("/mcp/update_job", h.MCPUpdateJob)
	router.POST("/mcp/delete_job", h.MCPDeleteJob)
	router.POST("/mcp/trigger_job", h.MCPTriggerJob)
	router.POST("/mcp/get_log", h.MCPGetLog)

	// Dispatch queue endpoints.
	router.GET("/dispatch/queue", h.ListDispatchQueue)
	router.GET("/dispatch/queue/:id", h.GetDispatchEntry)
	router.POST("/dispatch/queue/:id/retry", h.RetryDispatch)
	router.POST("/mcp/list_dispatch_queue", h.MCPListDispatchQueue)
	router.POST("/mcp/retry_dispatch", h.MCPRetryDispatch)

	// Push tools to MCP server when it becomes available.
	sdkClient.WhenPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
