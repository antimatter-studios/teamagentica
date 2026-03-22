package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-persona/internal/storage"
)

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

	router := gin.Default()
	h := handlers.New(db)

	// Health
	router.GET("/health", h.Health)

	// Persona REST API
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
