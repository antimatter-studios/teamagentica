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
	"github.com/antimatter-studios/teamagentica/plugins/infra-builder/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "infra-builder"
	}

	const defaultPort = 8090

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"build:docker"},
		Version:      pluginsdk.DevVersion("1.0.0"),
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"PLUGIN_PORT":  {Type: "number", Label: "Listen Port", Default: "8090", HelpText: "Port the builder listens on"},
			"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Enable debug logging", Order: 99},
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	if v := pluginConfig["PLUGIN_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	router := gin.Default()

	h := handlers.NewHandler(sdkClient, debug)

	router.GET("/health", h.Health)
	router.GET("/tools", h.Tools)
	router.POST("/tool/build", h.ToolBuild)
	router.POST("/build", h.Build)
	router.GET("/builds", h.ListBuilds)
	router.GET("/builds/:id/logs", h.GetBuildLogs)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
