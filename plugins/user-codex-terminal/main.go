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
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

	var sdkClient *pluginsdk.Client
	sdkClient = pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			// Read current config to merge into workspace env_defaults.
			approvalMode := "suggest"
			if cfg, err := sdkClient.FetchConfig(); err == nil {
				if v, ok := cfg["CODEX_APPROVAL_MODE"]; ok && v != "" {
					approvalMode = v
				}
			}

			return map[string]interface{}{
				"config":    getConfigSchema(),
				"workspace": getWorkspaceSchema(approvalMode),
			}
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

	log.Printf("user-codex-terminal listening on :%d", defaultPort)
	if err := r.Run(":8090"); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getConfigSchema() map[string]pluginsdk.ConfigSchemaField {
	return map[string]pluginsdk.ConfigSchemaField{
		"CODEX_APPROVAL_MODE": {
			Type:     "select",
			Label:    "Approval Mode",
			Default:  "suggest",
			Options:  []string{"suggest", "auto-edit", "full-auto"},
			HelpText: "suggest: asks before everything, auto-edit: auto-approves file changes, full-auto: auto-approves everything",
			Order:    1,
		},
		"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", Order: 99},
	}
}

func getWorkspaceSchema(approvalMode string) map[string]interface{} {
	return map[string]interface{}{
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
			"HOME":                "/home/coder",
			"CODEX_APPROVAL_MODE": approvalMode,
			"TACLI_KERNEL":        "http://teamagentica-kernel:8080",
		},
	}
}
