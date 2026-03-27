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

	h := handlers.New(nil)

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ToolsFunc: func() interface{} {
			return h.ToolDefs()
		},
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
			if h.DB() != nil {
				if roles, err := h.DB().ListRoles(); err == nil {
					schema["roles"] = map[string]interface{}{
						"_display": "table",
						"_columns": []string{"id", "label"},
						"items":    roles,
					}
				}
			}
			return schema
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	dataPath := "/data"

	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	router := gin.Default()
	h = handlers.New(db)

	// Health
	router.GET("/health", h.Health)

	// Persona REST API
	router.GET("/personas", h.ListPersonas)
	router.GET("/personas/default", h.GetDefaultPersona)
	router.GET("/personas/by-role/:role", h.GetPersonasByRole)
	router.GET("/personas/:alias", h.GetPersona)
	router.POST("/personas", h.CreatePersona)
	router.PUT("/personas/:alias", h.UpdatePersona)
	router.POST("/personas/:alias/set-default", h.SetDefaultPersona)
	router.DELETE("/personas/:alias", h.DeletePersona)

	// Role REST API
	router.GET("/roles", h.ListRoles)
	router.GET("/roles/:id", h.GetRole)
	router.POST("/roles", h.CreateRole)
	router.PUT("/roles/:id", h.UpdateRole)
	router.DELETE("/roles/:id", h.DeleteRole)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/list_personas", h.MCPListPersonas)
	router.POST("/mcp/get_persona", h.MCPGetPersona)
	router.POST("/mcp/create_persona", h.MCPCreatePersona)
	router.POST("/mcp/update_persona", h.MCPUpdatePersona)
	router.POST("/mcp/delete_persona", h.MCPDeletePersona)
	router.POST("/mcp/list_roles", h.MCPListRoles)
	router.POST("/mcp/get_persona_by_role", h.MCPGetPersonaByRole)
	router.POST("/mcp/assign_role", h.MCPAssignRole)
	router.POST("/mcp/get_default_persona", h.MCPGetDefaultPersona)
	router.POST("/mcp/set_default_persona", h.MCPSetDefaultPersona)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
