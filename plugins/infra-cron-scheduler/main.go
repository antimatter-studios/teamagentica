package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-cron-scheduler/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-cron-scheduler/internal/scheduler"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"SCHEDULER_PORT": {Type: "number", Label: "Listen Port", Default: "8081", HelpText: "Port the scheduler listens on"},
			"PLUGIN_DEBUG":   {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Enable debug logging", Order: 99},
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
	if v := pluginConfig["SCHEDULER_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	sched := scheduler.New()
	defer sched.Stop()

	router := gin.Default()
	h := handlers.NewHandler(sched)

	router.GET("/health", h.Health)
	router.POST("/events", h.CreateEvent)
	router.GET("/events", h.ListEvents)
	router.GET("/events/:id", h.GetEvent)
	router.PUT("/events/:id", h.UpdateEvent)
	router.DELETE("/events/:id", h.DeleteEvent)
	router.GET("/log", h.GetLog)

	h.SetSDK(sdkClient)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
