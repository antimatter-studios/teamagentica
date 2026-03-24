package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/storage"
)

//go:embed prompts/coordinator-system-prompt.md
var coordinatorPrompt string

//go:embed prompts/worker-system-prompt.md
var workerPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

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
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("failed to fetch plugin config: %v", err)
	}

	dataPath := "/data"
	if pluginConfig != nil {
		if v := pluginConfig["PLUGIN_DATA_PATH"]; v != "" {
			dataPath = v
		}
	}

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	// Seed coordinator persona if it doesn't exist yet.
	if _, err := db.Get("coordinator"); err != nil {
		prompt := strings.TrimSpace(coordinatorPrompt)
		if seedErr := db.Create(&storage.Persona{
			Alias:        "coordinator",
			SystemPrompt: prompt,
		}); seedErr != nil {
			log.Printf("failed to seed coordinator persona: %v", seedErr)
		} else {
			log.Println("seeded coordinator persona from embedded prompt")
		}
	}

	router := gin.Default()
	h := handlers.New(db, strings.TrimSpace(workerPrompt), strings.TrimSpace(coordinatorPrompt))

	// Health
	router.GET("/health", h.Health)

	// Persona REST API
	router.GET("/default-prompt", h.DefaultPrompt)
	router.GET("/personas", h.ListPersonas)
	router.GET("/personas/:alias", h.GetPersona)
	router.POST("/personas", h.CreatePersona)
	router.PUT("/personas/:alias", h.UpdatePersona)
	router.DELETE("/personas/:alias", h.DeletePersona)

	// MCP tool discovery + execution
	router.GET("/tools", h.GetTools)
	router.POST("/mcp/list_personas", h.MCPListPersonas)
	router.POST("/mcp/get_persona", h.MCPGetPersona)
	router.POST("/mcp/create_persona", h.MCPCreatePersona)
	router.POST("/mcp/update_persona", h.MCPUpdatePersona)
	router.POST("/mcp/delete_persona", h.MCPDeletePersona)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
