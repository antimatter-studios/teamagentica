package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/scheduler/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/scheduler/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/scheduler/internal/scheduler"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	sched := scheduler.New()
	defer sched.Stop()

	router := gin.Default()
	h := handlers.NewHandler(cfg, sched)

	router.GET("/health", h.Health)
	router.POST("/events", h.CreateEvent)
	router.GET("/events", h.ListEvents)
	router.GET("/events/:id", h.GetEvent)
	router.PUT("/events/:id", h.UpdateEvent)
	router.DELETE("/events/:id", h.DeleteEvent)
	router.GET("/log", h.GetLog)

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"tool:scheduler"},
		Version:      "1.0.0",
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

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
