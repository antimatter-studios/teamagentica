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
				"display_name": "OpenCode Terminal",
				"description":  "Web terminal with OpenCode CLI — AI-powered coding assistant",
				"icon":         `<svg viewBox="0 0 24 24" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="#6366F1"/><path d="M9 8l-4 4 4 4M15 8l4 4-4 4" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
				"image":        "teamagentica-devbox:latest",
				"port":         7681,
				"docker_user":  "",
				"shared_mounts": []map[string]interface{}{
					{"disk_name": "opencode-shared", "target": "/home/coder/.opencode"},
				},
				"env_defaults": map[string]string{
					"DEVBOX_APP":        "opencode",
					"DEFAULT_WORKSPACE": "/workspace",
					"HOME":             "/home/coder",
					"TACLI_KERNEL":      "http://teamagentica-kernel:8080",
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
			DisplayName: "OpenCode Terminal",
			Description: "Web terminal with OpenCode CLI — AI-powered coding assistant",
			Image:       "teamagentica-devbox:latest",
			Port:        7681,
			Icon:        `<svg viewBox="0 0 24 24" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="#6366F1"/><path d="M9 8l-4 4 4 4M15 8l4 4-4 4" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
			Disks: []events.WorkspaceDiskSpec{
				{Type: "workspace", Target: "/workspace"},
				{Type: "shared", Name: "opencode-shared", Target: "/home/coder/.opencode"},
			},
			EnvDefaults: map[string]string{
				"DEVBOX_APP":        "opencode",
				"DEFAULT_WORKSPACE": "/workspace",
				"HOME":              "/home/coder",
				"TACLI_KERNEL":      "http://teamagentica-kernel:8080",
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
