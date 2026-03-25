package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/storage"
	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/usercache"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	const httpPort = 8093

	var h *handlers.Handler

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
			return h.ToolDefs()
		},
	})

	sdkClient.Start(context.Background())

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("WARNING: failed to fetch plugin config: %v", err)
		pluginConfig = map[string]string{}
	}

	dataPath := pluginConfig["PLUGIN_DATA_PATH"]
	if dataPath == "" {
		dataPath = "/data"
	}

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	uc := usercache.New(sdkClient, 5*time.Minute)
	h = handlers.New(db, sdkClient, uc)

	router := gin.Default()
	router.GET("/health", h.Health)

	// Boards
	router.GET("/boards", h.ListBoards)
	router.POST("/boards", h.CreateBoard)
	router.GET("/boards/:id", h.GetBoard)
	router.PUT("/boards/:id", h.UpdateBoard)
	router.DELETE("/boards/:id", h.DeleteBoard)

	// Columns (nested under board)
	router.GET("/boards/:id/columns", h.ListColumns)
	router.POST("/boards/:id/columns", h.CreateColumn)
	router.PUT("/boards/:id/columns/:cid", h.UpdateColumn)
	router.DELETE("/boards/:id/columns/:cid", h.DeleteColumn)

	// Cards (nested under board)
	router.GET("/boards/:id/cards", h.ListCards)
	router.POST("/boards/:id/cards", h.CreateCard)
	router.PUT("/boards/:id/cards/:cid", h.UpdateCard)
	router.DELETE("/boards/:id/cards/:cid", h.DeleteCard)

	// Single card by ID
	router.GET("/cards/:cid", h.GetCard)

	// Comments (nested under card)
	router.GET("/cards/:cid/comments", h.ListComments)
	router.POST("/cards/:cid/comments", h.CreateComment)
	router.DELETE("/cards/:cid/comments/:cmid", h.DeleteComment)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_boards", h.MCPListBoards)
	router.POST("/mcp/list_tasks", h.MCPListTasks)
	router.POST("/mcp/list_tasks_by_status", h.MCPListTasksByStatus)
	router.POST("/mcp/create_task", h.MCPCreateTask)
	router.POST("/mcp/set_task_state", h.MCPSetTaskState)
	router.POST("/mcp/update_task", h.MCPUpdateTask)
	router.POST("/mcp/add_comment", h.MCPAddComment)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
