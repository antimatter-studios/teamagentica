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
	"github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/bot"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/kernel"
	waClient "github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/whatsapp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname := getHostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "messaging-whatsapp"
	}

	const httpPort = 8091

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         httpPort,
		Capabilities: []string{"messaging:whatsapp"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"WHATSAPP_ACCESS_TOKEN":    {Type: "string", Label: "Access Token", Required: true, Secret: true, HelpText: "Permanent access token from Meta developer portal", Order: 1},
			"WHATSAPP_PHONE_NUMBER_ID": {Type: "string", Label: "Phone Number ID", Required: true, HelpText: "WhatsApp Business phone number ID from Meta developer portal", Order: 2},
			"WHATSAPP_VERIFY_TOKEN":    {Type: "string", Label: "Webhook Verify Token", Required: true, HelpText: "A secret string you choose — must match what you enter in Meta's webhook configuration", Order: 3},
			"WHATSAPP_APP_SECRET":      {Type: "string", Label: "App Secret", Secret: true, HelpText: "Optional app secret for webhook signature verification", Order: 4},
			"DEFAULT_AGENT":            {Type: "select", Label: "Coordinator Agent", Dynamic: true, HelpText: "Select the default agent that acts as coordinator", Order: 5},
			"PLUGIN_DEBUG":             {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed request/response traffic", Order: 99},
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

	// Build the kernel base URL, respecting TLS setting.
	scheme := "http"
	if sdkCfg.TLSEnabled {
		scheme = "https"
	}
	kernelURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)
	kernelClient := kernel.NewClient(kernelURL, sdkCfg.PluginToken, debug)

	// WhatsApp Cloud API client.
	wa := waClient.NewClient(accessToken, phoneNumberID, debug)

	// Bot handler.
	b := bot.NewBot(wa, kernelClient, pluginID, debug, aliases)
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
		field := c.Param("field")
		if field == "DEFAULT_AGENT" {
			entries := aliases.List()
			var names []string
			for _, e := range entries {
				if e.Target.Type == alias.TargetAgent {
					names = append(names, e.Alias)
				}
			}
			c.JSON(http.StatusOK, gin.H{"options": names})
			return
		}
		c.JSON(http.StatusOK, gin.H{"options": []string{}})
	})

	// Webhook endpoints (Meta sends GET for verification, POST for messages).
	router.GET("/webhook", b.VerifyWebhook(verifyToken))
	router.POST("/webhook", b.HandleWebhook)

	// Subscribe to live alias updates from kernel (debounced — coalesce rapid updates).
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse kernel:alias:update detail: %v", err)
			return
		}
		aliases.Replace(detail.Aliases)
		log.Printf("Hot-swapped %d aliases (seq=%d)", len(detail.Aliases), event.Seq)
	}))

	// Subscribe to soft config updates for dynamic DEFAULT_AGENT changes (immediate).
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse config:update detail: %v", err)
			return
		}
		if agent, ok := detail.Config["DEFAULT_AGENT"]; ok {
			b.SetDefaultAgent(agent)
		}
	}))

	// When network-webhook-ingress broadcasts webhook:ready, send our route info.
	sdkClient.OnEvent("webhook:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		log.Printf("Received webhook:ready — sending route update to network-webhook-ingress")
		payload := map[string]interface{}{
			"plugin_id":   pluginID,
			"prefix":      "/" + pluginID,
			"target_host": hostname,
			"target_port": httpPort,
		}
		data, _ := json.Marshal(payload)
		sdkClient.ReportAddressedEvent("webhook:api:update", string(data), "network-webhook-ingress")
		log.Printf("Sent webhook:api:update to network-webhook-ingress: prefix=/%s", pluginID)
	}))

	// When network-webhook-ingress sends us our full webhook URL, log it.
	// (Meta manages actual webhook registration via their dashboard — we just log for visibility.)
	sdkClient.OnEvent("webhook:plugin:url", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("Failed to parse webhook:plugin:url: %v", err)
			return
		}
		log.Printf("Webhook URL assigned: %s/webhook", data.WebhookURL)
		sdkClient.ReportEvent("webhook:url", fmt.Sprintf("url=%s/webhook (Meta manages registration)", data.WebhookURL))
	}))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}
