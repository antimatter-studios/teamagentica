package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/mcp-server/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/mcp-server/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	router := gin.Default()
	h := handlers.NewHandler(cfg)

	// Register routes.
	router.GET("/health", h.Health)
	router.GET("/info", h.Info)
	router.POST("/mcp", h.MCP)

	// Create plugin SDK client and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"mcp:server"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"PLUGIN_DEBUG": {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed MCP protocol traffic", Order: 99},
		},
	})
	sdkClient.Start(context.Background())
	h.SetSDK(sdkClient)

	// Subscribe to live alias updates so send_message resolves correctly.
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("mcp-server: failed to parse alias update: %v", err)
			return
		}
		if mcpSrv := h.MCPServer(); mcpSrv != nil {
			mcpSrv.UpdateAliases(detail.Aliases)
			log.Printf("mcp-server: hot-swapped %d aliases", len(detail.Aliases))
		}
	}))

	endpoint := fmt.Sprintf("http://%s:%d/mcp", getHostname(), cfg.Port)

	// Broadcast mcp_server:enabled after registration.
	go func() {
		detail, _ := json.Marshal(map[string]string{"endpoint": endpoint})
		sdkClient.ReportEvent("mcp_server:enabled", string(detail))
		log.Printf("mcp-server: broadcast mcp_server:enabled endpoint=%s", endpoint)
	}()

	// Configure server-side TLS if enabled.
	tlsCfg, err := pluginsdk.GetServerTLSConfig(sdkCfg)
	if err != nil {
		log.Fatalf("mcp-server: failed to configure server TLS: %v", err)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
	}

	// Start server.
	go func() {
		if tlsCfg != nil {
			server.TLSConfig = tlsCfg
			log.Printf("mcp-server: listening on %s (mTLS)", server.Addr)
			if err := server.ListenAndServeTLS(sdkCfg.TLSCert, sdkCfg.TLSKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("mcp-server: server error: %v", err)
			}
		} else {
			log.Printf("mcp-server: listening on %s", server.Addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("mcp-server: server error: %v", err)
			}
		}
	}()

	// Wait for shutdown signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("mcp-server: received signal %s, shutting down", sig)

	// Broadcast mcp_server:disabled before deregistering.
	sdkClient.ReportEvent("mcp_server:disabled", "{}")
	log.Println("mcp-server: broadcast mcp_server:disabled")

	// Deregister from kernel.
	sdkClient.Stop()

	// Gracefully shut down HTTP server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("mcp-server: server shutdown error: %v", err)
	}

	log.Println("mcp-server: shutdown complete")
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
