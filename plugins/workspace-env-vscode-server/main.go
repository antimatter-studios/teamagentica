package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

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
		Schema: map[string]interface{}{
			"config": map[string]pluginsdk.ConfigSchemaField{
				"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
			},
			"workspace": map[string]interface{}{
				"display_name": "VS Code",
				"description":  "Full IDE with terminal, extensions, and git support",
				"icon":         `<svg viewBox="0 0 24 24" fill="none"><path d="M17.5 0L9.5 8 5 4.5 2 6v12l3 1.5L9.5 16l8 8 4.5-2V2L17.5 0zM5 14.5v-5l3 2.5-3 2.5zm9.5 2L9.5 12l5-4.5v9z" fill="#007ACC"/></svg>`,
				"image":        "teamagentica-code-server:latest",
				"port":         8080,
				"docker_user":  "coder",
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

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("WARNING: failed to fetch plugin config: %v", err)
	}

	if pluginConfig["PLUGIN_DEBUG"] != "true" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"plugin":  manifest.ID,
			"version": "1.0.0",
		})
	})

	// Register with workspace-manager when it signals ready.
	registerEnv := func() {
		payload := events.WorkspaceEnvironmentRegisterPayload{
			PluginID:    manifest.ID,
			DisplayName: "VS Code",
			Description: "Full IDE with terminal, extensions, and git support",
			Image:       "teamagentica-code-server:latest",
			Port:        8080,
			Icon:        `<svg viewBox="0 0 24 24" fill="none"><path d="M17.5 0L9.5 8 5 4.5 2 6v12l3 1.5L9.5 16l8 8 4.5-2V2L17.5 0zM5 14.5v-5l3 2.5-3 2.5zm9.5 2L9.5 12l5-4.5v9z" fill="#007ACC"/></svg>`,
			DockerUser:  "coder",
			SharedMounts: []events.WorkspaceExtraMount{
				{VolumeName: "code-server-shared/extensions", Target: "/mnt/shared-extensions"},
				{VolumeName: "claude-shared", Target: "/home/coder/.claude"},
			},
			EnvDefaults: map[string]string{
				"DEFAULT_WORKSPACE": "/workspace",
				"XDG_DATA_HOME":    "/workspace/.code-server",
			},
		}
		b, _ := json.Marshal(payload)
		sdkClient.Events().Publish("workspace:environment:register", string(b))
		log.Printf("registered workspace environment: %s", manifest.ID)
	}

	sdkClient.Events().On("workspace:manager:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		registerEnv()
	}))

	registerEnv()

	sdkClient.ListenAndServe(defaultPort, r)
}
