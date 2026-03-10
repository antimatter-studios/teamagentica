package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/audit"
	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/certs"
	"github.com/antimatter-studios/teamagentica/kernel/internal/config"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/handlers"
	"github.com/antimatter-studios/teamagentica/kernel/internal/health"
	"github.com/antimatter-studios/teamagentica/kernel/internal/middleware"
	"github.com/antimatter-studios/teamagentica/kernel/internal/orchestrator"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

const version = "0.1.0"

func main() {
	cfg := config.Load()

	// Ensure kernel data subdirectory exists.
	kernelDataDir := filepath.Join(cfg.DataDir, "kernel")
	if err := os.MkdirAll(kernelDataDir, 0o755); err != nil {
		log.Fatalf("failed to create kernel data dir: %v", err)
	}

	auth.InitJWT(cfg.JWTSecret)
	database.Init(cfg.DBPath)

	// SQLite online backups.
	backupCtx, backupCancel := context.WithCancel(context.Background())
	defer backupCancel()
	database.StartBackups(backupCtx, kernelDataDir, cfg.BackupInterval)

	// mTLS setup (optional).
	var certManager *certs.CertManager
	var serverTLS *tls.Config
	var clientTLS *tls.Config

	if cfg.MTLSEnabled {
		var err error
		certManager, err = certs.NewCertManager(kernelDataDir)
		if err != nil {
			log.Fatalf("failed to init cert manager: %v", err)
		}

		if _, _, err := certManager.GenerateKernelCert(); err != nil {
			log.Fatalf("failed to generate kernel cert: %v", err)
		}

		serverTLS, err = certManager.GetServerTLSConfig()
		if err != nil {
			log.Fatalf("failed to get server TLS config: %v", err)
		}

		clientTLS, err = certManager.GetClientTLSConfig()
		if err != nil {
			log.Fatalf("failed to get client TLS config: %v", err)
		}

		log.Println("mTLS enabled — kernel will serve HTTPS and proxy to plugins over HTTPS")
	} else {
		log.Println("mTLS disabled — running in plain HTTP mode")
	}

	// Audit logger.
	auditLogger := audit.NewLogger(database.DB)

	r := gin.Default()
	r.Use(middleware.CORS())
	r.Use(middleware.AuditInjector(auditLogger))

	// Health check.
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version,
			"app":     cfg.AppName,
		})
	})

	// Auth routes (public).
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/register", middleware.AuthOptional(), handlers.Register)
		authGroup.POST("/login", handlers.Login)
		authGroup.POST("/session", middleware.AuthRequired(), handlers.CreateSession)
	}

	// Service token routes (authenticated, admin only).
	serviceTokenGroup := r.Group("/api/auth")
	serviceTokenGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		serviceTokenGroup.POST("/service-token", handlers.CreateServiceToken)
		serviceTokenGroup.GET("/service-tokens", handlers.ListServiceTokens)
		serviceTokenGroup.DELETE("/service-token/:id", handlers.RevokeServiceToken)
	}

	// Audit log routes (admin only).
	auditGroup := r.Group("/api/audit")
	auditGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		auditGroup.GET("", handlers.ListAuditLogs)
	}

	// User routes (authenticated).
	usersGroup := r.Group("/api/users")
	usersGroup.Use(middleware.AuthRequired())
	{
		usersGroup.GET("/me", handlers.Me)
		usersGroup.GET("", middleware.RequireCapability("users:read"), handlers.ListUsers)
	}

	// Pricing routes (admin only).
	pricingHandler := handlers.NewPricingHandler(database.DB)
	pricingGroup := r.Group("/api/pricing")
	pricingGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		pricingGroup.GET("", pricingHandler.ListPrices)
		pricingGroup.GET("/current", pricingHandler.ListCurrentPrices)
		pricingGroup.POST("", pricingHandler.SavePrice)
		pricingGroup.DELETE("/:id", pricingHandler.DeletePrice)
	}

	// External user mapping routes (admin only).
	extUserHandler := handlers.NewExternalUserHandler(database.DB)
	extUserGroup := r.Group("/api/external-users")
	extUserGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		extUserGroup.GET("", extUserHandler.List)
		extUserGroup.POST("", extUserHandler.Create)
		extUserGroup.PUT("/:id", extUserHandler.Update)
		extUserGroup.DELETE("/:id", extUserHandler.Delete)
	}

	// Docker runtime (gracefully degrades if Docker unavailable).
	dockerRT, err := runtime.NewDockerRuntime(cfg.DockerNetwork, certManager, cfg.DevMode, cfg.BaseDomain)
	if err != nil {
		log.Fatalf("failed to init docker runtime: %v", err)
	}

	// Plugin routes (authenticated).
	pluginHandler := handlers.NewPluginHandler(database.DB, dockerRT, cfg, clientTLS)

	// Health monitor (started after pluginHandler so it can emit to the event hub).
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	monitor := health.NewMonitor(database.DB, dockerRT, pluginHandler.Events, 30*time.Second)
	go monitor.Start(monitorCtx)
	pluginsGroup := r.Group("/api/plugins")
	pluginsGroup.Use(middleware.AuthRequired())
	{
		pluginsGroup.GET("/search", middleware.RequireCapability("plugins:search"), pluginHandler.SearchPlugins)
		pluginsGroup.POST("", middleware.RequireCapability("plugins:manage"), pluginHandler.RegisterPlugin)
		pluginsGroup.GET("", middleware.RequireCapability("plugins:manage"), pluginHandler.ListPlugins)
		pluginsGroup.GET("/:id", middleware.RequireCapability("plugins:manage"), pluginHandler.GetPlugin)
		pluginsGroup.DELETE("/:id", middleware.RequireCapability("plugins:manage"), pluginHandler.UninstallPlugin)
		pluginsGroup.POST("/:id/enable", middleware.RequireCapability("plugins:manage"), pluginHandler.EnablePlugin)
		pluginsGroup.POST("/:id/disable", middleware.RequireCapability("plugins:manage"), pluginHandler.DisablePlugin)
		pluginsGroup.POST("/:id/restart", middleware.RequireCapability("plugins:manage"), pluginHandler.RestartPlugin)
		pluginsGroup.GET("/:id/schema", middleware.RequireCapability("plugins:search"), pluginHandler.GetPluginSchema)
		pluginsGroup.GET("/:id/schema/:section", middleware.RequireCapability("plugins:search"), pluginHandler.GetPluginSchemaSection)
		pluginsGroup.GET("/:id/logs", middleware.RequireCapability("plugins:manage"), pluginHandler.GetPluginLogs)
		pluginsGroup.GET("/:id/config", middleware.RequireCapability("plugins:manage"), pluginHandler.GetPluginConfig)
		pluginsGroup.PUT("/:id/config", middleware.RequireCapability("plugins:manage"), pluginHandler.UpdatePluginConfig)
	}

	// Marketplace routes (authenticated, plugins:manage).
	marketplaceHandler := handlers.NewMarketplaceHandler(database.DB, pluginHandler.Events)
	marketplaceGroup := r.Group("/api/marketplace")
	marketplaceGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("plugins:manage"))
	{
		marketplaceGroup.GET("/providers", marketplaceHandler.ListProviders)
		marketplaceGroup.POST("/providers", marketplaceHandler.AddProvider)
		marketplaceGroup.DELETE("/providers/:id", marketplaceHandler.DeleteProvider)
		marketplaceGroup.GET("/plugins", marketplaceHandler.BrowsePlugins)
		marketplaceGroup.POST("/install", marketplaceHandler.InstallPlugin)
	}

	// Alias routes — read available to plugin tokens, mutations require admin.
	aliasReadGroup := r.Group("/api/aliases")
	aliasReadGroup.Use(middleware.PluginTokenAuth())
	{
		aliasReadGroup.GET("", pluginHandler.ListAliases)
	}
	aliasAdminGroup := r.Group("/api/aliases")
	aliasAdminGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		aliasAdminGroup.POST("", pluginHandler.UpsertAlias)
		aliasAdminGroup.PUT("", pluginHandler.BulkReplaceAliases)
		aliasAdminGroup.DELETE("/:name", pluginHandler.DeleteAlias)
	}

	// Plugin self-registration routes (plugin token auth, not user auth).
	pluginRegGroup := r.Group("/api/plugins")
	pluginRegGroup.Use(middleware.PluginTokenAuth())
	{
		pluginRegGroup.POST("/register", pluginHandler.SelfRegister)
		pluginRegGroup.POST("/heartbeat", pluginHandler.Heartbeat)
		pluginRegGroup.POST("/deregister", pluginHandler.Deregister)
		pluginRegGroup.POST("/event", pluginHandler.ReportEvent)
		pluginRegGroup.POST("/subscribe", pluginHandler.SubscribeEvent)
		pluginRegGroup.POST("/unsubscribe", pluginHandler.UnsubscribeEvent)
		pluginRegGroup.POST("/pricing", pluginHandler.UpdatePricing)
		pluginRegGroup.GET("/:id/self-config", pluginHandler.GetSelfConfig)

		// Managed container routes (plugin-callable).
		pluginRegGroup.POST("/containers", pluginHandler.CreateManagedContainer)
		pluginRegGroup.GET("/containers", pluginHandler.ListManagedContainers)
		pluginRegGroup.GET("/containers/:cid", pluginHandler.GetManagedContainer)
		pluginRegGroup.PATCH("/containers/:cid", pluginHandler.UpdateManagedContainer)
		pluginRegGroup.DELETE("/containers/:cid", pluginHandler.DeleteManagedContainer)
		pluginRegGroup.GET("/containers/:cid/logs", pluginHandler.GetManagedContainerLogs)
	}

	// Admin managed-container routes.
	mcAdminGroup := r.Group("/api/managed-containers")
	mcAdminGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("plugins:manage"))
	{
		mcAdminGroup.GET("", pluginHandler.ListAllManagedContainers)
		mcAdminGroup.DELETE("/:id", pluginHandler.ForceDeleteManagedContainer)
	}

	// Plugin routing/proxy (user authenticated, kernel proxies to plugin).
	routeGroup := r.Group("/api/route")
	routeGroup.Use(middleware.AuthRequired())
	{
		routeGroup.Any("/:plugin_id/*path", pluginHandler.RouteToPlugin)
	}

	// Public webhook ingress — no auth required.
	// External services (Telegram, Discord, etc.) POST here via ngrok tunnel.
	webhookGroup := r.Group("/api/webhook")
	{
		webhookGroup.Any("/:plugin_id/*path", pluginHandler.WebhookIngress)
	}

	// Debug console SSE (admin only).
	debugGroup := r.Group("/api/debug")
	debugGroup.Use(middleware.AuthRequired(), middleware.RequireCapability("system:admin"))
	{
		debugGroup.GET("/events", handlers.DebugEventsSSE(pluginHandler.Events))
		debugGroup.GET("/history", handlers.DebugEventsHistory(pluginHandler.Events))
		debugGroup.GET("/event-log", handlers.DebugEventLog(database.DB))
		debugGroup.GET("/test", handlers.DebugEventsTest(pluginHandler.Events))
	}

	// Boot orchestrator: start all enabled plugins in background.
	orch := orchestrator.NewOrchestrator(database.DB, dockerRT, cfg, pluginHandler.Events)
	monitor.SetRestarter(orch)
	go func() {
		orchCtx, orchCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer orchCancel()
		if err := orch.StartEnabledPlugins(orchCtx); err != nil {
			log.Printf("orchestrator: boot error: %v", err)
		}
		orch.RecoverManagedContainers(orchCtx)
	}()

	addr := cfg.Host + ":" + cfg.Port

	var server *http.Server
	if cfg.MTLSEnabled && serverTLS != nil {
		server = &http.Server{
			Addr:      addr,
			Handler:   r,
			TLSConfig: serverTLS,
		}
		certPath := kernelDataDir + "/certs/kernel.crt"
		keyPath := kernelDataDir + "/certs/kernel.key"
		log.Printf("%s kernel starting on https://%s (v%s)\n", cfg.AppName, addr, version)
		go func() {
			if err := server.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server failed: %v", err)
			}
		}()
	} else {
		server = &http.Server{
			Addr:    addr,
			Handler: r,
		}
		log.Printf("%s kernel starting on http://%s (v%s)\n", cfg.AppName, addr, version)
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server failed: %v", err)
			}
		}()
	}

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")

	// Stop backups and health monitor.
	backupCancel()
	monitorCancel()

	// Stop all plugins.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := orch.StopAllPlugins(shutdownCtx); err != nil {
		log.Printf("orchestrator: shutdown error: %v", err)
	}

	// Shutdown HTTP server.
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Println("kernel stopped")
}
