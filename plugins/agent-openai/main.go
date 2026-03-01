package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"roboslop/pkg/pluginsdk"
	"roboslop/plugins/agent-openai/internal/config"
	"roboslop/plugins/agent-openai/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	// Set up Gin router.
	router := gin.Default()

	// Create handler with config.
	h := handlers.NewHandler(cfg)

	// Register routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)

	// Create plugin SDK client and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"ai:chat", "ai:chat:openai"},
		Version:      "1.0.0",
	})
	sdkClient.Start(context.Background())

	// Run server with graceful shutdown.
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
