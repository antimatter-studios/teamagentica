package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/ngrok/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/ngrok/internal/tunnel"

	"context"
)

var (
	tunnelURL   string
	tunnelURLMu sync.RWMutex
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	hostname, _ := os.Hostname()

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         hostname,
		Port:         cfg.HTTPPort,
		Capabilities: []string{"tunnel:ngrok"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"NGROK_AUTHTOKEN":     {Type: "string", Label: "ngrok Auth Token", Required: true, Secret: true, HelpText: "Your ngrok authentication token from https://dashboard.ngrok.com"},
			"NGROK_DOMAIN":        {Type: "string", Label: "Custom Domain", HelpText: "Optional static ngrok domain (e.g. my-app.ngrok-free.app). Leave empty for a random URL."},
			"NGROK_TUNNEL_TARGET": {Type: "string", Label: "Tunnel Target", HelpText: "Internal host:port to tunnel to. Leave empty to use the kernel. Set to webhook-ingress host:port if using the webhook ingress plugin (e.g. teamagentica-plugin-webhook-ingress:9000)."},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	// Start ngrok tunnel.
	mgr := tunnel.NewManager(cfg.NgrokAuthToken, cfg.NgrokDomain, cfg.TunnelTarget)

	url, err := mgr.Start(ctx)
	if err != nil {
		log.Fatalf("Failed to start ngrok tunnel: %v", err)
	}
	log.Printf("ngrok tunnel established: %s -> %s", url, cfg.TunnelTarget)

	tunnelURLMu.Lock()
	tunnelURL = url
	tunnelURLMu.Unlock()

	// Send addressed event to webhook-ingress (kernel queues if it's not up yet).
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
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: mux,
	}

	log.Printf("ngrok plugin starting on :%d", cfg.HTTPPort)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)

	if err := mgr.Close(); err != nil {
		log.Printf("Error closing tunnel: %v", err)
	}
	log.Println("ngrok plugin shut down")
}

// reportTunnelUpdate sends an addressed webhook:tunnel:update event to webhook-ingress.
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

	client.ReportAddressedEvent("webhook:tunnel:update", string(data), "webhook-ingress")
	log.Printf("Sent webhook:tunnel:update to webhook-ingress: %s", string(data))
}
