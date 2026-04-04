package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/relay"
	waClient "github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/whatsapp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname := getHostname()
	manifest := pluginsdk.LoadManifest()

	const httpPort = 8091

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	// Seed aliases from kernel (will update dynamically via alias:update events).
	entries, fetchErr := sdkClient.FetchAliases()
	if fetchErr != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", fetchErr)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	// Start SDK (register with kernel + heartbeat loop + event server + subscriptions).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	accessToken := pluginConfig["WHATSAPP_ACCESS_TOKEN"]
	phoneNumberID := pluginConfig["WHATSAPP_PHONE_NUMBER_ID"]
	verifyToken := pluginConfig["WHATSAPP_VERIFY_TOKEN"]
	debug := pluginConfig["PLUGIN_DEBUG"] == "true" || pluginConfig["PLUGIN_DEBUG"] == "1"

	// Relay client for routing messages through infra-agent-relay.
	rc := relay.NewClient(sdkClient, manifest.ID)

	// WhatsApp Cloud API client.
	wa := waClient.NewClient(accessToken, phoneNumberID, debug)

	// Bot handler.
	b := bot.NewBot(wa, rc, manifest.ID, debug, aliases)
	b.SetSDK(sdkClient)

	router := gin.Default()
	// Health check.
	router.GET("/health", func(c *gin.Context) {
		configured := accessToken != "" && phoneNumberID != ""
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"plugin":     "messaging-whatsapp",
			"version":    "1.0.0",
			"configured": configured,
		})
	})

	// Config options endpoint for dynamic selects.
	router.GET("/config/options/:field", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"options": []string{}})
	})

	// Webhook endpoints (Meta sends GET for verification, POST for messages).
	router.GET("/webhook", b.VerifyWebhook(verifyToken))
	router.POST("/webhook", b.HandleWebhook)

	// Handler for alias registry events (update + ready).
	handleAliasEvent := func(event pluginsdk.EventCallback) {
		infos := convertRegistryAliases(event.Detail)
		if infos == nil {
			log.Printf("Failed to parse alias registry event detail")
			return
		}
		aliases.Replace(infos)
		log.Printf("Hot-swapped %d aliases from registry (seq=%d)", len(infos), event.Seq)
	}

	// Subscribe to alias updates from infra-alias-registry.
	sdkClient.Events().On("alias-registry:update", pluginsdk.NewTimedDebouncer(2*time.Second, handleAliasEvent))
	sdkClient.Events().On("alias-registry:ready", pluginsdk.NewTimedDebouncer(1*time.Second, handleAliasEvent))

	// Subscribe to config updates.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		// Future: handle PLUGIN_DEBUG toggle here.
		_ = p
	})

	// Register webhook route with the webhooks plugin (handles webhook:ready subscription).
	sdkClient.RegisterWebhook("/" + manifest.ID)

	// When webhooks plugin sends us our full webhook URL, log it.
	// (Meta manages actual webhook registration via their dashboard — we just log for visibility.)
	sdkClient.OnWebhookURL(func(webhookURL string) {
		log.Printf("Webhook URL assigned: %s/webhook", webhookURL)
		events.PublishStatus(sdkClient, "webhook:url", fmt.Sprintf("url=%s/webhook (Meta manages registration)", webhookURL))
	})

	sdkClient.ListenAndServe(httpPort, router)
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
		var caps []string
		switch e.Type {
		case "agent":
			caps = []string{"agent:chat"}
		case "tool_agent":
			caps = []string{"agent:tool"}
		default:
			caps = []string{"tool:mcp"}
		}
		infos = append(infos, alias.AliasInfo{
			Name:         e.Name,
			Target:       target,
			Capabilities: caps,
		})
	}
	return infos
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}
