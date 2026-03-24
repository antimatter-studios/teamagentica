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
	"github.com/antimatter-studios/teamagentica/plugins/network-ngrok-ingress/internal/tunnel"
)

var (
	tunnelURL   string
	tunnelURLMu sync.RWMutex

	// mutable config guarded by cfgMu
	cfgMu        sync.RWMutex
	cfgAuthToken string
	cfgDomain    string
	cfgTarget    string // auto-discovered from webhook plugin
	activeMgr    *tunnel.Manager
	activeCtx    context.Context
	activeSdk    *pluginsdk.Client
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 9100

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": getConfigSchema(),
				"status": getStatusSchema(),
			}
		},
	})

	// Auto-discover the webhook plugin via webhook:ready events.
	// The webhook plugin broadcasts this with {host, port} on startup and
	// whenever a new plugin registers.
	sdkClient.OnEvent("webhook:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("[webhook] failed to parse webhook:ready: %v", err)
			return
		}
		if data.Host == "" || data.Port == 0 {
			return
		}

		newTarget := fmt.Sprintf("%s:%d", data.Host, data.Port)

		cfgMu.Lock()
		if cfgTarget == newTarget {
			cfgMu.Unlock()
			return
		}
		oldTarget := cfgTarget
		cfgTarget = newTarget
		cfgMu.Unlock()

		if oldTarget == "" {
			log.Printf("[webhook] discovered webhook plugin at %s", newTarget)
		} else {
			log.Printf("[webhook] webhook plugin changed: %s -> %s", oldTarget, newTarget)
		}

		tryStartTunnel()
	}))

	// Respond to ingress:request with ingress:ready so other plugins can
	// discover the public URL on demand.
	sdkClient.OnEvent("ingress:request", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		log.Printf("[ingress] received ingress:request — broadcasting ingress:ready")
		broadcastIngressReady(sdkClient)
	}))

	// Hot-reload: listen for config:update events from kernel.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("[config] failed to parse config:update detail: %v", err)
			return
		}
		applyConfig(detail.Config)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	httpPort := defaultPort
	if v := pluginConfig["NGROK_HTTP_PORT"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			httpPort = p
		}
	}

	cfgMu.Lock()
	cfgAuthToken = pluginConfig["NGROK_AUTHTOKEN"]
	cfgDomain = pluginConfig["NGROK_DOMAIN"]
	activeCtx = ctx
	activeSdk = sdkClient
	cfgMu.Unlock()

	if cfgAuthToken == "" {
		log.Printf("WARNING: NGROK_AUTHTOKEN not configured — running idle, set it in plugin settings")
	} else {
		log.Printf("Waiting for webhook plugin discovery before starting tunnel...")
	}

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

	log.Printf("ngrok ingress starting on :%d", httpPort)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)

	cfgMu.RLock()
	mgr := activeMgr
	cfgMu.RUnlock()
	if mgr != nil {
		if err := mgr.Close(); err != nil {
			log.Printf("Error closing tunnel: %v", err)
		}
	}
	log.Println("ngrok ingress shut down")
}

// tryStartTunnel starts or restarts the tunnel if both auth token and target are known.
func tryStartTunnel() {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	if cfgAuthToken == "" {
		log.Printf("[tunnel] no auth token — cannot start tunnel")
		return
	}
	if cfgTarget == "" {
		log.Printf("[tunnel] no webhook plugin discovered — cannot start tunnel")
		return
	}

	// Close existing tunnel if any.
	if activeMgr != nil {
		if err := activeMgr.Close(); err != nil {
			log.Printf("[tunnel] error closing old tunnel: %v", err)
		}
		activeMgr = nil
		tunnelURLMu.Lock()
		tunnelURL = ""
		tunnelURLMu.Unlock()
	}

	mgr := tunnel.NewManager(cfgAuthToken, cfgDomain, cfgTarget)
	url, err := mgr.Start(activeCtx)
	if err != nil {
		log.Printf("[tunnel] failed to start: %v", err)
		return
	}

	activeMgr = mgr
	log.Printf("[tunnel] established: %s -> %s", url, cfgTarget)

	tunnelURLMu.Lock()
	tunnelURL = url
	tunnelURLMu.Unlock()

	// Broadcast ingress:ready so all plugins know the public URL.
	broadcastIngressReady(activeSdk)
}

// broadcastIngressReady broadcasts the public ingress URL to all plugins.
func broadcastIngressReady(client *pluginsdk.Client) {
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
		log.Printf("Failed to marshal ingress:ready: %v", err)
		return
	}

	client.ReportEvent("ingress:ready", string(data))
	log.Printf("Broadcast ingress:ready: %s", string(data))
}

// applyConfig hot-reloads config values from a config:update event.
func applyConfig(config map[string]string) {
	cfgMu.Lock()
	newAuth := config["NGROK_AUTHTOKEN"]
	newDomain := config["NGROK_DOMAIN"]

	changed := newAuth != cfgAuthToken || newDomain != cfgDomain
	cfgAuthToken = newAuth
	cfgDomain = newDomain
	cfgMu.Unlock()

	log.Printf("[config] applied: domain=%q authtoken=%v", newDomain, newAuth != "")

	if changed {
		tryStartTunnel()
	}
}

func getConfigSchema() map[string]pluginsdk.ConfigSchemaField {
	return map[string]pluginsdk.ConfigSchemaField{
		"NGROK_AUTHTOKEN": {Type: "string", Label: "ngrok Auth Token", Required: true, Secret: true, HelpText: "Your ngrok authentication token from https://dashboard.ngrok.com"},
		"NGROK_DOMAIN":    {Type: "string", Label: "Custom Domain", HelpText: "Optional static ngrok domain (e.g. my-app.ngrok-free.app). Leave empty for a random URL."},
		"NGROK_HTTP_PORT": {Type: "number", Label: "HTTP Port", Default: "9100", HelpText: "Port for the ngrok ingress health endpoint"},
	}
}

func getStatusSchema() map[string]string {
	tunnelURLMu.RLock()
	url := tunnelURL
	tunnelURLMu.RUnlock()

	cfgMu.RLock()
	target := cfgTarget
	cfgMu.RUnlock()

	if url == "" {
		url = "(not connected)"
	}
	if target == "" {
		target = "(waiting for webhook plugin)"
	}

	return map[string]string{
		"Public URL": url,
		"Target":     target,
	}
}
