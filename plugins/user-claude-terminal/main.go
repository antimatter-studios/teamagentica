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
		pluginID = "user-claude-terminal"
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
				"CLAUDE_SKIP_PERMISSIONS": {
					Type:     "boolean",
					Label:    "Bypass Permissions",
					Default:  "false",
					HelpText: "Run Claude Code with --dangerously-skip-permissions (auto-approves all tool use)",
					Order:    1,
				},
				"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
			},
		},
		SchemaFunc: func() map[string]interface{} {
			skipPermissions := "false"
			if cfg, err := sdkClient.FetchConfig(); err == nil {
				if v, ok := cfg["CLAUDE_SKIP_PERMISSIONS"]; ok && v != "" {
					skipPermissions = v
				}
			}

			return map[string]interface{}{
				"config": map[string]pluginsdk.ConfigSchemaField{
					"CLAUDE_SKIP_PERMISSIONS": {
						Type:     "boolean",
						Label:    "Bypass Permissions",
						Default:  "false",
						HelpText: "Run Claude Code with --dangerously-skip-permissions (auto-approves all tool use)",
						Order:    1,
					},
					"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
				},
				"workspace": map[string]interface{}{
					"display_name": "Claude Terminal",
					"description":  "Web terminal with Claude Code CLI — AI-powered coding assistant",
					"icon":         `<svg viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="10" fill="#D97706"/><path d="M8 10c0-1.1.9-2 2-2h4c1.1 0 2 .9 2 2v2c0 1.1-.9 2-2 2h-4c-1.1 0-2-.9-2-2v-2z" fill="#fff"/><rect x="9" y="15" width="6" height="2" rx="1" fill="#fff"/></svg>`,
					"image":        "teamagentica-devbox:latest",
					"port":         7681,
					"docker_user":  "",
					"shared_mounts": []map[string]interface{}{
						{"volume_name": "claude-shared", "target": "/home/coder/.claude"},
					},
					"env_defaults": map[string]string{
						"DEVBOX_APP":              "claude",
						"DEFAULT_WORKSPACE":       "/workspace",
						"HOME":                   "/home/coder",
						"CLAUDE_SKIP_PERMISSIONS": skipPermissions,
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

	log.Printf("user-claude-terminal listening on :%d", defaultPort)
	if err := r.Run(":8090"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
