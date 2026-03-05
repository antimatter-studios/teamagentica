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
	"github.com/antimatter-studios/teamagentica/plugins/cost-explorer/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/cost-explorer/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/cost-explorer/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	db, err := storage.Open(cfg.DataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	router := gin.Default()

	h := handlers.NewHandler(db)

	router.GET("/health", h.Health)
	router.POST("/usage", h.ReportUsage)
	router.GET("/usage", h.Summary)
	router.GET("/usage/records", h.ListRecords)
	router.GET("/usage/users", h.UsageUsers)
	router.POST("/events/usage", h.HandleUsageEvent)

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"system:cost-explorer"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Enable debug logging", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

	// Subscribe to usage:report events with retry.
	go func() {
		for i := 0; i < 30; i++ {
			if err := sdkClient.Subscribe("usage:report", "/events/usage"); err != nil {
				log.Printf("subscribe failed (attempt %d): %v", i+1, err)
				time.Sleep(2 * time.Second)
				continue
			}
			log.Println("subscribed to usage:report events")
			return
		}
		log.Println("WARNING: failed to subscribe to usage:report after 30 attempts")
	}()

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
