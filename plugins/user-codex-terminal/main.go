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

	var sdkClient *pluginsdk.Client
	sdkClient = pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"workspace:environment"},
		Version:      pluginsdk.DevVersion("1.0.0"),
		Schema: map[string]interface{}{
			"config": map[string]pluginsdk.ConfigSchemaField{
				"CODEX_APPROVAL_MODE": {
					Type:     "select",
					Label:    "Approval Mode",
					Default:  "suggest",
					Options:  []string{"suggest", "auto-edit", "full-auto"},
					HelpText: "suggest: asks before everything, auto-edit: auto-approves file changes, full-auto: auto-approves everything",
					Order:    1,
				},
				"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
			},
		},
		SchemaFunc: func() map[string]interface{} {
			// Read current config to merge into workspace env_defaults.
			approvalMode := "suggest"
			if cfg, err := sdkClient.FetchConfig(); err == nil {
				if v, ok := cfg["CODEX_APPROVAL_MODE"]; ok && v != "" {
					approvalMode = v
				}
			}

			return map[string]interface{}{
				"config": map[string]pluginsdk.ConfigSchemaField{
					"CODEX_APPROVAL_MODE": {
						Type:     "select",
						Label:    "Approval Mode",
						Default:  "suggest",
						Options:  []string{"suggest", "auto-edit", "full-auto"},
						HelpText: "suggest: asks before everything, auto-edit: auto-approves file changes, full-auto: auto-approves everything",
						Order:    1,
					},
					"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
				},
				"workspace": map[string]interface{}{
					"display_name": "Codex Terminal",
					"description":  "Web terminal with OpenAI Codex CLI — AI-powered coding assistant",
					"icon":         `<svg viewBox="0 0 24 24" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="#10A37F"/><path d="M12 6v12M8 10l4-4 4 4M8 14l4 4 4-4" stroke="#fff" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
					"image":        "teamagentica-devbox:latest",
					"port":         7681,
					"docker_user":  "",
					"shared_mounts": []map[string]interface{}{
						{"volume_name": "codex-shared", "target": "/home/coder/.codex"},
					},
					"env_defaults": map[string]string{
						"DEVBOX_APP":          "codex",
						"DEFAULT_WORKSPACE":   "/workspace",
						"HOME":               "/home/coder",
						"CODEX_APPROVAL_MODE": approvalMode,
					},
				},
			}
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
