package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/network-ngrok/internal/tunnel"
)

var (
	tunnelURL   string
	tunnelURLMu sync.RWMutex
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 9100

	configSchema := map[string]pluginsdk.ConfigSchemaField{
		"NGROK_AUTHTOKEN":     {Type: "string", Label: "ngrok Auth Token", Required: true, Secret: true, HelpText: "Your ngrok authentication token from https://dashboard.ngrok.com"},
		"NGROK_DOMAIN":        {Type: "string", Label: "Custom Domain", HelpText: "Optional static ngrok domain (e.g. my-app.ngrok-free.app). Leave empty for a random URL."},
		"NGROK_TUNNEL_TARGET": {Type: "string", Label: "Tunnel Target", HelpText: "Internal host:port to tunnel to. Leave empty to use the kernel. Set to network-webhook-ingress host:port if using the webhook ingress plugin (e.g. teamagentica-plugin-network-webhook-ingress:9000)."},
		"NGROK_HTTP_PORT":     {Type: "number", Label: "HTTP Port", Default: "9100", HelpText: "Port for the ngrok plugin health endpoint"},
	}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			tunnelURLMu.RLock()
			url := tunnelURL
			tunnelURLMu.RUnlock()

			if url == "" {
				url = "(not connected)"
			}

			return map[string]interface{}{
				"config": configSchema,
				"status": map[string]string{
					"Public URL": url,
				},
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

	ngrokAuthToken := pluginConfig["NGROK_AUTHTOKEN"]
	if ngrokAuthToken == "" {
		log.Fatalf("NGROK_AUTHTOKEN not configured — set it in the plugin settings")
	}

	ngrokDomain := pluginConfig["NGROK_DOMAIN"]

	tunnelTarget := pluginConfig["NGROK_TUNNEL_TARGET"]
	if tunnelTarget == "" {
		tunnelTarget = fmt.Sprintf("%s:%s", sdkCfg.KernelHost, sdkCfg.KernelPort)
	}

	httpPort := defaultPort
	if v := pluginConfig["NGROK_HTTP_PORT"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			httpPort = p
		}
	}

	// Start ngrok tunnel.
	mgr := tunnel.NewManager(ngrokAuthToken, ngrokDomain, tunnelTarget)

	url, err := mgr.Start(ctx)
	if err != nil {
		log.Fatalf("Failed to start ngrok tunnel: %v", err)
	}
	log.Printf("ngrok tunnel established: %s -> %s", url, tunnelTarget)

	tunnelURLMu.Lock()
	tunnelURL = url
	tunnelURLMu.Unlock()

	// Send addressed event to network-webhook-ingress (kernel queues if it's not up yet).
	reportTunnelUpdate(sdkClient)

	// Minimal HTTP server for health.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		tunnelURLMu.RLock()
		u := tunnelURL
		tunnelURLMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","tunnel_url":%q}`, u)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}

	log.Printf("ngrok plugin starting on :%d", httpPort)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)

	if err := mgr.Close(); err != nil {
		log.Printf("Error closing tunnel: %v", err)
	}
	log.Println("ngrok plugin shut down")
}

// reportTunnelUpdate sends an addressed webhook:tunnel:update event to network-webhook-ingress.
func reportTunnelUpdate(client *pluginsdk.Client) {
	tunnelURLMu.RLock()
	url := tunnelURL
	tunnelURLMu.RUnlock()

	if url == "" {
		return
	}

	payload := map[string]string{
		"url":   url,
		"proto": "https",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal tunnel update: %v", err)
		return
	}

	client.ReportAddressedEvent("webhook:tunnel:update", string(data), "network-webhook-ingress")
	log.Printf("Sent webhook:tunnel:update to network-webhook-ingress: %s", string(data))
}
