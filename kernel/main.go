package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/kernel/internal/audit"
	"github.com/antimatter-studios/teamagentica/kernel/internal/certs"
	"github.com/antimatter-studios/teamagentica/kernel/internal/config"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/handlers"
	"github.com/antimatter-studios/teamagentica/kernel/internal/health"
	"github.com/antimatter-studios/teamagentica/kernel/internal/middleware"
	"github.com/antimatter-studios/teamagentica/kernel/internal/orchestrator"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime/runtimecfg"
	"github.com/antimatter-studios/teamagentica/kernel/internal/watchdog"
)

const version = "0.1.0"

func main() {
	cfg := config.Load()

	// Ensure kernel data subdirectory exists.
	// The kernel container always has its data at /data (bind-mounted from DataDir on the host).
	kernelDataDir := "/data/kernel"
	if err := os.MkdirAll(kernelDataDir, 0o755); err != nil {
		log.Fatalf("failed to create kernel data dir: %v", err)
	}

	database.Init(cfg.DBPath)

	// SQLite online backups.
	backupCtx, backupCancel := context.WithCancel(context.Background())
	defer backupCancel()
	database.StartBackups(backupCtx, kernelDataDir, cfg.BackupInterval)

	// mTLS — always enabled. Kernel generates CA and certs for itself and all plugins.
	certManager, err := certs.NewCertManager(kernelDataDir)
	if err != nil {
		log.Fatalf("failed to init cert manager: %v", err)
	}

	if _, _, err := certManager.GenerateKernelCert(); err != nil {
		log.Fatalf("failed to generate kernel cert: %v", err)
	}

	serverTLS, err := certManager.GetServerTLSConfig()
	if err != nil {
		log.Fatalf("failed to get server TLS config: %v", err)
	}

	clientTLS, err := certManager.GetClientTLSConfig()
	if err != nil {
		log.Fatalf("failed to get client TLS config: %v", err)
	}

	// Audit logger.
	auditLogger := audit.NewLogger()

	r := gin.Default()
	r.Use(middleware.CORS(cfg.BaseDomain))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.AuditInjector(auditLogger))

	// Health check.
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version,
			"app":     cfg.AppName,
		})
	})

	// Load runtime config (dev vs prod mounts, env vars, etc.).
	rtCfg, err := runtimecfg.Load()
	if err != nil {
		log.Fatalf("failed to load runtime config: %v", err)
	}

	// Docker runtime (gracefully degrades if Docker unavailable).
	dockerRT, err := runtime.NewDockerRuntime(cfg.DockerNetwork, cfg.DataDir, certManager, rtCfg, cfg.BaseDomain)
	if err != nil {
		log.Fatalf("failed to init docker runtime: %v", err)
	}

	// Plugin routes (authenticated).
	pluginHandler := handlers.NewPluginHandler(dockerRT, cfg, clientTLS)

	// --- Auth/User routes — proxied to system-user-manager plugin ---
	// Rate limiters for auth endpoints to prevent brute-force attacks.
	loginLimiter := middleware.NewRateLimiter(10, 1*time.Minute)  // 10 per minute per IP
	registerLimiter := middleware.NewRateLimiter(5, 1*time.Hour)  // 5 per hour per IP

	authProxy := pluginHandler.SystemPluginProxy("system-user-manager", "/api/auth")
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/register", registerLimiter.Middleware(), middleware.AuthOptional(), authProxy)
		authGroup.POST("/login", loginLimiter.Middleware(), authProxy)
		authGroup.POST("/session", middleware.AuthRequired(), authProxy)
		authGroup.POST("/refresh", loginLimiter.Middleware(), authProxy)
		authGroup.POST("/logout", middleware.AuthRequired(), authProxy)
	}
	auditProxy := pluginHandler.SystemPluginProxy("system-user-manager", "/api/audit")
	auditGroup := r.Group("/api/audit")
	auditGroup.Use(middleware.AuthRequired())
	{
		auditGroup.GET("", auditProxy)
	}

	usersProxy := pluginHandler.SystemPluginProxy("system-user-manager", "/api/users")
	usersGroup := r.Group("/api/users")
	usersGroup.Use(middleware.AuthRequired())
	{
		usersGroup.GET("/me", usersProxy)
		usersGroup.GET("", usersProxy)
		usersGroup.GET("/:id", usersProxy)
		usersGroup.PUT("/:id", usersProxy)
		usersGroup.PUT("/:id/ban", usersProxy)
		usersGroup.DELETE("/:id", usersProxy)
	}

	extUsersProxy := pluginHandler.SystemPluginProxy("system-user-manager", "/api/external-users")
	extUserGroup := r.Group("/api/external-users")
	extUserGroup.Use(middleware.AuthRequired())
	{
		extUserGroup.GET("", extUsersProxy)
		extUserGroup.GET("/lookup", extUsersProxy)
		extUserGroup.POST("", extUsersProxy)
		extUserGroup.PUT("/:id", extUsersProxy)
		extUserGroup.DELETE("/:id", extUsersProxy)
	}

	// Health monitor (started after pluginHandler so it can emit to the event hub).
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	monitor := health.NewMonitor(dockerRT, pluginHandler.Events, 30*time.Second)
	go monitor.Start(monitorCtx)

	// Plugin watchdog — detects disconnected plugins and coordinates re-registration
	// via the heartbeat signal. Complements the health monitor: health monitor handles
	// dead containers, plugin watchdog handles alive-but-disconnected ones.
	pluginWatchdog := watchdog.NewPluginWatchdog(dockerRT, 30*time.Second, database.Get)
	go pluginWatchdog.Start(monitorCtx)
	pluginsGroup := r.Group("/api/plugins")
	pluginsGroup.Use(middleware.AuthRequired())
	{
		pluginsGroup.GET("/search", pluginHandler.SearchPlugins)
		pluginsGroup.GET("/registry", pluginHandler.GetPluginRegistry)
		pluginsGroup.POST("", pluginHandler.RegisterPlugin)
		pluginsGroup.GET("", pluginHandler.ListPlugins)
		pluginsGroup.GET("/:id", pluginHandler.GetPlugin)
		pluginsGroup.GET("/:id/status", pluginHandler.GetPluginStatus)
		pluginsGroup.GET("/:id/address", pluginHandler.GetPluginAddress)
		pluginsGroup.DELETE("/:id", pluginHandler.UninstallPlugin)
		pluginsGroup.POST("/:id/enable", pluginHandler.EnablePlugin)
		pluginsGroup.POST("/:id/disable", pluginHandler.DisablePlugin)
		pluginsGroup.POST("/:id/restart", pluginHandler.RestartPlugin)
		pluginsGroup.GET("/:id/schema", pluginHandler.GetPluginSchema)
		pluginsGroup.GET("/:id/schema/:section", pluginHandler.GetPluginSchemaSection)
		pluginsGroup.GET("/:id/logs", pluginHandler.GetPluginLogs)
		pluginsGroup.GET("/:id/config", pluginHandler.GetPluginConfig)
		pluginsGroup.PUT("/:id/config", pluginHandler.UpdatePluginConfig)
		pluginsGroup.DELETE("/:id/config/:key", pluginHandler.DeletePluginConfigKey)
		pluginsGroup.POST("/:id/deploy", pluginHandler.DeployCandidate)
		pluginsGroup.POST("/:id/promote", pluginHandler.PromoteCandidate)
		pluginsGroup.POST("/:id/rollback", pluginHandler.RollbackCandidate)
	}

	// Marketplace routes (authenticated, plugins:manage).
	marketplaceClient := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}
	marketplaceHandler := handlers.NewMarketplaceHandler(pluginHandler.Events, marketplaceClient)
	marketplaceGroup := r.Group("/api/marketplace")
	marketplaceGroup.Use(middleware.AuthRequired())
	{
		marketplaceGroup.GET("/providers", marketplaceHandler.ListProviders)
		marketplaceGroup.POST("/providers", marketplaceHandler.AddProvider)
		marketplaceGroup.DELETE("/providers/:id", marketplaceHandler.DeleteProvider)
		marketplaceGroup.GET("/providers/:name/plugins", marketplaceHandler.ProviderPlugins)
		marketplaceGroup.GET("/plugins", marketplaceHandler.BrowsePlugins)
		marketplaceGroup.POST("/manifests", marketplaceHandler.SubmitManifest)
		marketplaceGroup.DELETE("/manifests/:id", marketplaceHandler.DeleteManifest)
		marketplaceGroup.POST("/install", marketplaceHandler.InstallPlugin)
		marketplaceGroup.POST("/upgrade", marketplaceHandler.UpgradePlugin)
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
		pluginRegGroup.POST("/containers/:cid/start", pluginHandler.StartManagedContainer)
		pluginRegGroup.POST("/containers/:cid/stop", pluginHandler.StopManagedContainer)
		pluginRegGroup.GET("/containers/:cid/logs", pluginHandler.GetManagedContainerLogs)

		// Deploy/promote/rollback (plugin-callable for automation).
		pluginRegGroup.POST("/deploy/:id", pluginHandler.DeployCandidate)
		pluginRegGroup.POST("/promote/:id", pluginHandler.PromoteCandidate)
		pluginRegGroup.POST("/rollback/:id", pluginHandler.RollbackCandidate)
	}

	// Admin managed-container routes.
	mcAdminGroup := r.Group("/api/managed-containers")
	mcAdminGroup.Use(middleware.AuthRequired())
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

	// Workspace proxy — path-based routing to managed containers.
	// Enables single-origin access (no subdomain dependency), works behind any gateway.
	// No auth required — matches existing subdomain routing (docker-proxy goes directly
	// to containers without kernel auth). Workspace IDs are random and unguessable.
	wsGroup := r.Group("/ws")
	{
		wsGroup.Any("/:container_id/*path", pluginHandler.ProxyToManagedContainer)
	}

	// Public webhook ingress — no auth required.
	// External services (Telegram, Discord, etc.) POST here via ngrok tunnel.
	webhookGroup := r.Group("/api/webhook")
	{
		webhookGroup.Any("/:plugin_id/*path", pluginHandler.WebhookIngress)
	}

	// Kernel self-inspection routes (admin only).
	kernelGroup := r.Group("/api/kernel")
	kernelGroup.Use(middleware.AuthRequired())
	{
		kernelGroup.GET("/logs", pluginHandler.GetKernelLogs)
		kernelGroup.GET("/ui/logs", pluginHandler.GetUILogs)
	}

	// Boot orchestrator: start all enabled plugins in background.
	orch := orchestrator.NewOrchestrator(dockerRT, cfg, pluginHandler.Events, clientTLS)
	monitor.SetRestarter(orch)
	monitor.SetBroadcaster(func(eventType string, detail map[string]interface{}) {
		pluginHandler.BroadcastLifecycleEventPublic(eventType, detail)
	})
	go func() {
		orchCtx, orchCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer orchCancel()
		if err := orch.StartEnabledPlugins(orchCtx); err != nil {
			log.Printf("orchestrator: boot error: %v", err)
		}
		orch.MigrateDiskMountsToSourcePath(orchCtx)
		orch.RecoverManagedContainers(orchCtx)

		// Wait for plugins to register, then broadcast full address registry
		// so all plugins can populate their peer caches for P2P communication.
		time.Sleep(10 * time.Second)
		pluginHandler.BroadcastRegistrySync()

		// Wire up event publisher so kernel debug events flow to infra-redis SSE.
		pluginHandler.EnableEventPublisher()

		// Fetch JWT secret from the user-manager plugin.
		// The plugin owns the secret; the kernel only needs it for middleware validation.
		if err := fetchJWTSecretFromPlugin(clientTLS); err != nil {
			log.Printf("WARNING: failed to fetch JWT secret from user-manager: %v", err)
			log.Printf("WARNING: auth middleware will reject all tokens until the plugin is reachable")
		}
	}()

	// Two listeners:
	// 1. HTTP  on cfg.Port (8080) — user traffic (browser, tacli, external API)
	// 2. HTTPS on cfg.Port+1 (8081) — plugin traffic (mTLS, Docker network only)
	httpAddr := cfg.Host + ":" + cfg.Port
	server := &http.Server{
		Addr:    httpAddr,
		Handler: r,
	}
	log.Printf("%s kernel starting on http://%s (v%s)\n", cfg.AppName, httpAddr, version)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failed: %v", err)
		}
	}()

	certPath := kernelDataDir + "/certs/kernel.crt"
	keyPath := kernelDataDir + "/certs/kernel.key"
	tlsAddr := cfg.Host + ":" + cfg.TLSPort
	tlsServer := &http.Server{
		Addr:      tlsAddr,
		Handler:   r,
		TLSConfig: serverTLS,
	}
	log.Printf("%s kernel mTLS on https://%s\n", cfg.AppName, tlsAddr)
	go func() {
		if err := tlsServer.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
			log.Fatalf("tls server failed: %v", err)
		}
	}()

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

	// Shutdown both HTTP servers.
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}
	if err := tlsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("tls server shutdown error: %v", err)
	}

	log.Println("kernel stopped")
}
