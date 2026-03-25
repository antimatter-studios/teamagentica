package main

import (
	"context"
	_ "embed"
	"encoding/json"
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

//go:embed prompts/coordinator.md
var coordinatorPrompt string

//go:embed prompts/worker.md
var workerPrompt string

//go:embed prompts/memory-extraction.md
var memoryExtractionPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	h := handlers.New(nil, strings.TrimSpace(workerPrompt), strings.TrimSpace(coordinatorPrompt))

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
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
				"roles": map[string]interface{}{
					"description": "Persona roles with cardinality constraints",
					"endpoints": map[string]string{
						"list":   "GET /roles",
						"get":    "GET /roles/:id",
						"create": "POST /roles",
						"update": "PUT /roles/:id",
						"delete": "DELETE /roles/:id",
					},
				},
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

	// Build role seeds with embedded prompts.
	roleSeeds := []storage.RoleSeed{
		{ID: "coordinator", Label: "Coordinator", MaxCount: 1, SystemPrompt: strings.TrimSpace(coordinatorPrompt)},
		{ID: "memory", Label: "Memory Extraction", MaxCount: 1, SystemPrompt: strings.TrimSpace(memoryExtractionPrompt)},
		{ID: "worker", Label: "Worker", MaxCount: 0, SystemPrompt: strings.TrimSpace(workerPrompt)},
	}

	// Seed default roles (creates if not exists).
	if err := db.SeedRoles(roleSeeds); err != nil {
		log.Printf("failed to seed roles: %v", err)
	}

	// Seed coordinator persona if it doesn't exist yet.
	if _, err := db.Get("coordinator"); err != nil {
		prompt := strings.TrimSpace(coordinatorPrompt)
		if seedErr := db.Create(&storage.Persona{
			Alias:        "coordinator",
			SystemPrompt: prompt,
			Role:         "coordinator",
		}); seedErr != nil {
			log.Printf("failed to seed coordinator persona: %v", seedErr)
		} else {
			log.Println("seeded coordinator persona from embedded prompt")
		}
	} else {
		// Ensure existing coordinator persona has the coordinator role.
		existing, _ := db.Get("coordinator")
		if existing != nil && existing.Role != "coordinator" {
			if err := db.AssignRole("coordinator", "coordinator"); err != nil {
				log.Printf("failed to assign coordinator role: %v", err)
			}
		}
	}

	router := gin.Default()
	h = handlers.New(db, strings.TrimSpace(workerPrompt), strings.TrimSpace(coordinatorPrompt))

	// Health
	router.GET("/health", h.Health)

	// Persona REST API
	router.GET("/default-prompt", h.DefaultPrompt)
	router.GET("/personas", h.ListPersonas)
	router.GET("/personas/:alias", h.GetPersona)
	router.POST("/personas", h.CreatePersona)
	router.PUT("/personas/:alias", h.UpdatePersona)
	router.DELETE("/personas/:alias", h.DeletePersona)
	router.GET("/personas/by-role/:role", h.GetPersonasByRole)

	// Role REST API
	router.GET("/roles", h.ListRoles)
	router.GET("/roles/:id", h.GetRole)
	router.POST("/roles", h.CreateRole)
	router.PUT("/roles/:id", h.UpdateRole)
	router.DELETE("/roles/:id", h.DeleteRole)

	// Reset all role prompts to embedded defaults.
	router.POST("/roles/reset-prompts", func(c *gin.Context) {
		log.Println("reset-prompts triggered — resetting role prompts to embedded defaults")
		if err := db.ResetRolePrompts(roleSeeds); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "role prompts reset to defaults"})
	})

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

	// Handle RESET_PROMPTS via config:update — no restart needed.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			return
		}
		if detail.Config["RESET_PROMPTS"] == "true" {
			log.Println("RESET_PROMPTS triggered — resetting role prompts to embedded defaults")
			if err := db.ResetRolePrompts(roleSeeds); err != nil {
				log.Printf("failed to reset role prompts: %v", err)
			} else {
				log.Println("role prompts reset successfully")
			}
		}
	}))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
