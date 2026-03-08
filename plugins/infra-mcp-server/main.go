package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-mcp-server/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "infra-mcp-server"
	}

	const defaultPort = 8081

	// Create plugin SDK client and register with kernel.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"mcp:server"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"MCP_SERVER_PORT": {Type: "number", Label: "Listen Port", Default: "8081", HelpText: "Port the MCP server listens on"},
			"PLUGIN_DEBUG":    {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed MCP protocol traffic", Order: 99},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	if v := pluginConfig["MCP_SERVER_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	router := gin.Default()
	h := handlers.NewHandler(pluginID, debug)

	// Register routes.
	router.GET("/health", h.Health)
	router.GET("/info", h.Info)
	router.GET("/tools", h.Tools)
	router.POST("/mcp", h.MCP)

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

	endpoint := fmt.Sprintf("http://%s:%d/mcp", hostname, port)

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
		Addr:    fmt.Sprintf(":%d", port),
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("mcp-server: server shutdown error: %v", err)
	}

	log.Println("mcp-server: shutdown complete")
}
