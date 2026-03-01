package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"roboslop/kernel/internal/auth"
	"roboslop/kernel/internal/certs"
	"roboslop/kernel/internal/config"
	"roboslop/kernel/internal/database"
	"roboslop/kernel/internal/handlers"
	"roboslop/kernel/internal/health"
	"roboslop/kernel/internal/middleware"
	"roboslop/kernel/internal/runtime"
)

const version = "0.1.0"

func main() {
	cfg := config.Load()

	auth.InitJWT(cfg.JWTSecret)
	database.Init(cfg.DBPath)

	// mTLS setup (optional).
	var certManager *certs.CertManager
	var serverTLS *tls.Config
	var clientTLS *tls.Config

	if cfg.MTLSEnabled {
		var err error
		certManager, err = certs.NewCertManager(cfg.DataDir)
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

	r := gin.Default()
	r.Use(middleware.CORS())

	// Health check.
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version,
		})
	})

	// Auth routes (public).
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/register", handlers.Register)
		authGroup.POST("/login", handlers.Login)
	}

	// User routes (authenticated).
	usersGroup := r.Group("/api/users")
	usersGroup.Use(middleware.AuthRequired())
	{
		usersGroup.GET("/me", handlers.Me)
		usersGroup.GET("", middleware.RequireCapability("users:read"), handlers.ListUsers)
	}

	// Docker runtime (gracefully degrades if Docker unavailable).
	dockerRT, err := runtime.NewDockerRuntime(cfg.DockerNetwork, certManager)
	if err != nil {
		log.Fatalf("failed to init docker runtime: %v", err)
	}

	// Health monitor.
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	monitor := health.NewMonitor(database.DB, dockerRT, 30*time.Second)
	go monitor.Start(monitorCtx)

	// Plugin routes (authenticated).
	pluginHandler := handlers.NewPluginHandler(database.DB, dockerRT, cfg, clientTLS)
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
		pluginsGroup.GET("/:id/logs", middleware.RequireCapability("plugins:manage"), pluginHandler.GetPluginLogs)
		pluginsGroup.GET("/:id/config", middleware.RequireCapability("plugins:manage"), pluginHandler.GetPluginConfig)
		pluginsGroup.PUT("/:id/config", middleware.RequireCapability("plugins:manage"), pluginHandler.UpdatePluginConfig)
	}

	// Plugin self-registration routes (plugin token auth, not user auth).
	pluginRegGroup := r.Group("/api/plugins")
	pluginRegGroup.Use(middleware.PluginTokenAuth())
	{
		pluginRegGroup.POST("/register", pluginHandler.SelfRegister)
		pluginRegGroup.POST("/heartbeat", pluginHandler.Heartbeat)
		pluginRegGroup.POST("/deregister", pluginHandler.Deregister)
	}

	// Plugin routing/proxy (user authenticated, kernel proxies to plugin).
	routeGroup := r.Group("/api/route")
	routeGroup.Use(middleware.AuthRequired())
	{
		routeGroup.Any("/:plugin_id/*path", pluginHandler.RouteToPlugin)
	}

	addr := cfg.Host + ":" + cfg.Port
	if cfg.MTLSEnabled && serverTLS != nil {
		server := &http.Server{
			Addr:      addr,
			Handler:   r,
			TLSConfig: serverTLS,
		}
		certPath := cfg.DataDir + "/certs/kernel.crt"
		keyPath := cfg.DataDir + "/certs/kernel.key"
		log.Printf("kernel starting on https://%s (v%s)\n", addr, version)
		if err := server.ListenAndServeTLS(certPath, keyPath); err != nil {
			log.Fatalf("server failed: %v", err)
		}
	} else {
		log.Printf("kernel starting on http://%s (v%s)\n", addr, version)
		if err := r.Run(addr); err != nil {
			log.Fatalf("server failed: %v", err)
		}
	}
}
