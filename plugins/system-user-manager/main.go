package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/audit"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/auth"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
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
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	port := defaultPort
	dataPath := "/data"

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("failed to fetch plugin config: %v", err)
	}

	// --- Open database (before JWT — secret is stored here) ---
	db, err := storage.Open(dataPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	// --- Bootstrap JWT secret ---
	// The plugin owns the JWT secret. On first boot it generates one and
	// stores it in its own database. The kernel fetches it via GET /internal/jwt-secret.
	jwtSecret, err := db.GetSetting("jwt_secret")
	if err != nil || jwtSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("failed to generate JWT secret: %v", err)
		}
		jwtSecret = hex.EncodeToString(b)
		if err := db.SetSetting("jwt_secret", jwtSecret); err != nil {
			log.Fatalf("failed to persist JWT secret: %v", err)
		}
		log.Println("jwt: new secret generated and stored")
	} else {
		log.Println("jwt: secret loaded from database")
	}

	expiryHours := 24
	if v := pluginConfig["JWT_EXPIRY_HOURS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			expiryHours = n
		}
	}

	auth.InitJWT(jwtSecret, expiryHours)

	// --- Initialize audit logger ---
	auditLogger := audit.NewLogger(db)

	// --- Create router ---
	router := gin.Default()
	h := handlers.New(db, sdkClient, auditLogger)

	// Health
	router.GET("/health", h.Health)

	// Internal — kernel fetches JWT secret on boot (Docker-network only, not proxied).
	router.GET("/internal/jwt-secret", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"secret": jwtSecret})
	})

	// Auth (public — kernel proxies these without requiring auth)
	router.POST("/auth/login", h.Login)
	router.POST("/auth/register", h.Register)
	router.POST("/auth/session", h.CreateSession)

	// Service tokens (admin only — kernel enforces auth before proxying)
	router.POST("/auth/service-token", h.CreateServiceToken)
	router.GET("/auth/service-tokens", h.ListServiceTokens)
	router.DELETE("/auth/service-token/:id", h.RevokeServiceToken)

	// User management
	router.GET("/users/me", h.Me)
	router.GET("/users", h.ListUsers)
	router.GET("/users/:id", h.GetUser)
	router.PUT("/users/:id", h.UpdateUser)
	router.PUT("/users/:id/ban", h.BanUser)
	router.DELETE("/users/:id", h.DeleteUser)

	// External user mappings
	router.GET("/external-users", h.ListExternalUsers)
	router.GET("/external-users/lookup", h.LookupExternalUser)
	router.POST("/external-users", h.CreateExternalUser)
	router.PUT("/external-users/:id", h.UpdateExternalUser)
	router.DELETE("/external-users/:id", h.DeleteExternalUser)

	// Audit logs
	router.GET("/audit", h.ListAuditLogs)

	// --- Start server ---
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}
