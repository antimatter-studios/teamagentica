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
		pluginID = "user-vscode-server"
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
				"display_name":  "VS Code",
				"description":   "Full IDE with terminal, extensions, and git support",
				"image":       "teamagentica-code-server:latest",
				"port":        8080,
				"docker_user": "coder",
				"setup_scripts": []string{"code-server-navigator"},
				"shared_mounts": []map[string]interface{}{
					{"volume_name": "code-server-shared/extensions", "target": "/mnt/shared-extensions"},
					{"volume_name": "claude-shared", "target": "/home/coder/.claude"},
				},
				"env_defaults": map[string]string{
					"DEFAULT_WORKSPACE": "/workspace",
					"XDG_DATA_HOME":    "/workspace/.code-server",
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

	log.Printf("user-vscode-server listening on :%d", defaultPort)
	if err := r.Run(":8090"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
