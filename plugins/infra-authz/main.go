package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/authz"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/database"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	// Placeholder handler for ToolsFunc (will be replaced after DB init)
	h := handlers.New(nil, nil, nil, 60)

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
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	dataPath := "/data"

	expiryMinutes := 60
	if v, ok := pluginConfig["TOKEN_EXPIRY_MINUTES"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			expiryMinutes = n
		}
	}

	// Open database
	db, err := database.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	// Seed default RBAC roles (upsert, safe on restart).
	if err := db.SeedDefaultRoles(); err != nil {
		log.Printf("WARNING: failed to seed default roles: %v", err)
	}

	// Init token service (loads or generates Ed25519 keypair)
	tokenService, err := authz.NewTokenService(dataPath)
	if err != nil {
		log.Fatalf("failed to init token service: %v", err)
	}

	// Init policy engine
	policy := authz.NewPolicyEngine(db)

	// Create handler with all dependencies
	h = handlers.New(db, policy, tokenService, expiryMinutes)

	if v := os.Getenv("AUDIT_SAMPLE_RATE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			h.SetSampleRate(n)
		}
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if n, err := db.ExpireOldGrants(); err != nil {
				log.Printf("elevation expiry error: %v", err)
			} else if n > 0 {
				log.Printf("expired %d elevation grants", n)
			}
		}
	}()

	router := gin.Default()

	// Health
	router.GET("/health", h.Health)

	// Identity registration
	router.POST("/identity/register", h.RegisterIdentity)

	// Token endpoints
	router.POST("/token/mint", h.MintToken)
	router.POST("/token/verify", h.VerifyToken)
	router.GET("/jwks", h.JWKS)

	// Policy check
	router.POST("/check", h.Check)

	// Scope catalog
	router.GET("/scopes", h.ListScopes)

	// Roles
	router.GET("/roles", h.ListRoles)
	router.POST("/roles", h.CreateRole)
	router.PUT("/roles/:id", h.UpdateRole)

	// Grants
	router.POST("/grants", h.CreateGrant)

	// Audit
	router.GET("/audit", h.ListAudit)
	router.GET("/audit/stats", h.AuditStats)
	router.GET("/audit/verify", h.AuditVerify)
	router.POST("/audit/report", h.AuditReport)

	// Elevation
	router.POST("/elevation/request", h.RequestElevation)
	router.POST("/elevation/approve", h.ApproveElevation)
	router.GET("/elevation/grants", h.ListElevationGrants)
	router.GET("/elevation/grants/:id", h.GetElevationGrant)
	router.POST("/elevation/revoke", h.RevokeElevation)

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/check_scope", h.MCPCheckScope)
	router.POST("/mcp/mint_token", h.MCPMintToken)
	router.POST("/mcp/list_scopes", h.MCPListScopes)

	sdkClient.ListenAndServe(defaultPort, router)
}
