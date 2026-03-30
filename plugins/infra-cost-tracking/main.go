package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-cost-tracking/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-cost-tracking/internal/storage"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

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

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	dataPath := "/data"

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	router := gin.Default()

	h := handlers.NewHandler(db)

	// SDK helper handlers.
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))

	router.GET("/health", h.Health)
	router.POST("/usage", h.ReportUsage)
	router.GET("/usage", h.Summary)
	router.GET("/usage/records", h.ListRecords)
	router.GET("/usage/users", h.UsageUsers)

	h.SetSDK(sdkClient)

	// Subscribe to usage:report events via the standard event bus.
	sdkClient.OnEvent("usage:report", pluginsdk.NewNullDebouncer(h.ProcessUsageEvent))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
