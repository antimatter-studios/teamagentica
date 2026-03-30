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
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/infra-mcp-server/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	// Create plugin SDK client and register with kernel.
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
	h := handlers.NewHandler(manifest.ID, debug)

	// SDK helper handlers.
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))

	// Register routes.
	router.GET("/health", h.Health)
	router.GET("/info", h.Info)
	// MCP protocol: POST (client requests), GET (SSE), DELETE (session cleanup).
	router.Any("/mcp", h.MCP)
	// Push-based tool registration from plugins.
	router.POST("/tools/register", h.RegisterTools)

	h.SetSDK(sdkClient)

	// Handler for alias registry events (update + ready).
	handleAliasEvent := func(event pluginsdk.EventCallback) {
		infos := convertRegistryAliases(event.Detail)
		if infos == nil {
			log.Printf("mcp-server: failed to parse alias registry event detail")
			return
		}
		if mcpSrv := h.MCPServer(); mcpSrv != nil {
			mcpSrv.UpdateAliases(infos)
			log.Printf("mcp-server: hot-swapped %d aliases from registry", len(infos))
		}
	}

	// Subscribe to alias updates from infra-alias-registry.
	sdkClient.OnEvent("alias-registry:update", pluginsdk.NewTimedDebouncer(2*time.Second, handleAliasEvent))
	sdkClient.OnEvent("alias-registry:ready", pluginsdk.NewTimedDebouncer(1*time.Second, handleAliasEvent))

	endpoint := fmt.Sprintf("http://%s:%d/mcp", hostname, port)

	// Broadcast mcp_server:enabled after registration.
	go func() {
		events.PublishMCPEnabled(sdkClient, endpoint)
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
	events.PublishMCPDisabled(sdkClient)
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

// convertRegistryAliases converts the alias registry event detail into []alias.AliasInfo.
// Registry shape: {"aliases": [{name, type, plugin, provider, model, system_prompt, ...}]}
func convertRegistryAliases(detail string) []alias.AliasInfo {
	var payload struct {
		Aliases []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Plugin string `json:"plugin"`
			Model  string `json:"model"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return nil
	}
	infos := make([]alias.AliasInfo, 0, len(payload.Aliases))
	for _, e := range payload.Aliases {
		target := e.Plugin
		if e.Model != "" {
			target = e.Plugin + ":" + e.Model
		}
		caps := []string{"tool:mcp"}
		if e.Type == "agent" {
			caps = []string{"agent:chat", "tool:mcp"}
		}
		infos = append(infos, alias.AliasInfo{
			Name:         e.Name,
			Target:       target,
			Capabilities: caps,
		})
	}
	return infos
}
