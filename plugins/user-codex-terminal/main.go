package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "user-codex-terminal"
	}

	const defaultPort = 8090

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"workspace:environment"},
		Version:      "1.0.0",
		Schema: map[string]interface{}{
			"config": map[string]pluginsdk.ConfigSchemaField{
				"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
			},
			"workspace": map[string]interface{}{
				"display_name": "Codex Terminal",
				"description":  "Web terminal with OpenAI Codex CLI — AI-powered coding assistant",
				"image":        "teamagentica-devbox:latest",
				"port":         7681,
				"docker_user":  "",
				"shared_mounts": []map[string]interface{}{},
				"env_defaults": map[string]string{
					"DEVBOX_APP":        "codex",
					"DEFAULT_WORKSPACE": "/workspace",
					"HOME":             "/home/coder",
				},
			},
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	if os.Getenv("PLUGIN_DEBUG") != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"plugin":  pluginID,
			"version": "1.0.0",
		})
	})

	log.Printf("user-codex-terminal listening on :%d", defaultPort)
	if err := r.Run(":8090"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
