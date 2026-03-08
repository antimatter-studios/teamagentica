package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Route represents a registered webhook route.
type Route struct {
	PluginID   string `json:"plugin_id"`
	Prefix     string `json:"prefix"`      // e.g. "/telegram-bot"
	TargetHost string `json:"target_host"` // internal hostname
	TargetPort int    `json:"target_port"` // internal port
}

var (
	routes   []Route
	routesMu sync.RWMutex
	baseURL  string // public base URL from tunnel provider
	baseURLMu sync.RWMutex
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load SDK config from env (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_PLUGIN_TOKEN, etc.)
	sdkCfg := pluginsdk.LoadConfig()

	hostname, _ := os.Hostname()
	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "network-webhook-ingress"
	}

	const defaultPort = 9000
	httpPort := defaultPort

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: []string{"webhook:ingress", "webhook:routing"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"WEBHOOK_INGRESS_PORT": {Type: "number", Label: "Listen Port", Default: "9000", HelpText: "Port the ingress listens on for external webhook traffic"},
		},
	})

	// --- Event handlers (registered before Start so SDK subscribes automatically) ---

	// Handle tunnel URL updates from ngrok (addressed delivery).
	sdkClient.OnEvent("webhook:tunnel:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			URL   string `json:"url"`
			Proto string `json:"proto"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("Failed to parse webhook:tunnel:update: %v", err)
			return
		}
		if data.URL == "" {
			log.Printf("webhook:tunnel:update has empty URL")
			return
		}

		baseURLMu.Lock()
		baseURL = strings.TrimRight(data.URL, "/")
		baseURLMu.Unlock()

		log.Printf("Tunnel URL updated: %s", data.URL)
		sdkClient.ReportEvent("webhook:tunnel", fmt.Sprintf("base_url=%s", data.URL))

		evaluateAndNotify(sdkClient)
	}))

	// Handle route updates from gateway plugins (addressed delivery).
	sdkClient.OnEvent("webhook:api:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			PluginID   string `json:"plugin_id"`
			Prefix     string `json:"prefix"`
			TargetHost string `json:"target_host"`
			TargetPort int    `json:"target_port"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("Failed to parse webhook:api:update: %v", err)
			return
		}
		if data.PluginID == "" || data.Prefix == "" {
			log.Printf("webhook:api:update missing plugin_id or prefix")
			return
		}

		route := Route{
			PluginID:   data.PluginID,
			Prefix:     NormalizePrefix(data.Prefix),
			TargetHost: data.TargetHost,
			TargetPort: data.TargetPort,
		}
		upsertRoute(route)

		log.Printf("Route updated via event: %s → %s:%d (prefix=%s)", data.PluginID, data.TargetHost, data.TargetPort, route.Prefix)
		sdkClient.ReportEvent("webhook:route", fmt.Sprintf("plugin=%s prefix=%s target=%s:%d", data.PluginID, route.Prefix, data.TargetHost, data.TargetPort))

		evaluateAndNotifyPlugin(sdkClient, data.PluginID, route.Prefix)
	}))

	// Re-broadcast webhook:ready when any plugin registers (so late joiners hear it).
	sdkClient.OnEvent("plugin:registered", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			PluginID string `json:"plugin_id"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse plugin:registered: %v", err)
			return
		}
		log.Printf("Plugin %s registered — re-broadcasting webhook:ready", detail.PluginID)
		broadcastWebhookReady(sdkClient, hostname, httpPort)
	}))

	// Start SDK (registration + heartbeat + event server).
	sdkClient.Start(context.Background())

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	if v := pluginConfig["WEBHOOK_INGRESS_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			httpPort = n
		}
	}

	// Broadcast initial webhook:ready after registration completes.
	go func() {
		time.Sleep(2 * time.Second)
		broadcastWebhookReady(sdkClient, hostname, httpPort)
	}()

	// Set up HTTP routes.
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		routesMu.RLock()
		count := len(routes)
		routesMu.RUnlock()
		baseURLMu.RLock()
		url := baseURL
		baseURLMu.RUnlock()
		fmt.Fprintf(w, `{"status":"ok","routes":%d,"base_url":%q}`, count, url)
	})

	// Route registration — called by plugins via kernel proxy (backwards compat).
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var req Route
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}

		if req.PluginID == "" || req.Prefix == "" || req.TargetHost == "" || req.TargetPort == 0 {
			http.Error(w, `{"error":"plugin_id, prefix, target_host, target_port required"}`, http.StatusBadRequest)
			return
		}

		req.Prefix = NormalizePrefix(req.Prefix)
		upsertRoute(req)

		sdkClient.ReportEvent("webhook:register", fmt.Sprintf("plugin=%s prefix=%s target=%s:%d", req.PluginID, req.Prefix, req.TargetHost, req.TargetPort))

		baseURLMu.RLock()
		url := baseURL
		baseURLMu.RUnlock()

		publicURL := ""
		if url != "" {
			publicURL = strings.TrimRight(url, "/") + req.Prefix
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":     "registered",
			"prefix":     req.Prefix,
			"public_url": publicURL,
		})

		log.Printf("Route registered: %s → %s:%d (prefix=%s, public=%s)", req.PluginID, req.TargetHost, req.TargetPort, req.Prefix, publicURL)
	})

	// Route unregistration.
	mux.HandleFunc("POST /unregister", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PluginID string `json:"plugin_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PluginID == "" {
			http.Error(w, `{"error":"plugin_id required"}`, http.StatusBadRequest)
			return
		}

		routesMu.Lock()
		filtered := routes[:0]
		for _, rt := range routes {
			if rt.PluginID != req.PluginID {
				filtered = append(filtered, rt)
			}
		}
		routes = filtered
		routesMu.Unlock()

		sdkClient.ReportEvent("webhook:unregister", fmt.Sprintf("plugin=%s", req.PluginID))

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"unregistered","plugin_id":%q}`, req.PluginID)
		log.Printf("Route unregistered: %s", req.PluginID)
	})

	// List routes (debug).
	mux.HandleFunc("GET /routes", func(w http.ResponseWriter, r *http.Request) {
		routesMu.RLock()
		defer routesMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(routes)
	})

	// Catch-all: route external webhook traffic to plugins based on prefix.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		routesMu.RLock()
		matched := MatchRoute(routes, path)
		routesMu.RUnlock()

		if matched == nil {
			http.Error(w, `{"error":"no route matched"}`, http.StatusNotFound)
			return
		}

		// Strip the prefix and forward the remainder.
		remainingPath := BuildRemainingPath(path, matched.Prefix)
		targetURL := BuildTargetURL(matched.TargetHost, matched.TargetPort, remainingPath, r.URL.RawQuery)

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, `{"error":"proxy error"}`, http.StatusInternalServerError)
			return
		}

		// Forward headers.
		for key, vals := range r.Header {
			for _, val := range vals {
				proxyReq.Header.Add(key, val)
			}
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(proxyReq)
		if err != nil {
			sdkClient.ReportEvent("webhook:error", fmt.Sprintf("plugin=%s path=%s error=%v", matched.PluginID, path, err))
			http.Error(w, `{"error":"target unreachable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response.
		for key, vals := range resp.Header {
			for _, val := range vals {
				w.Header().Add(key, val)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}

	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
	log.Println("Webhook ingress shut down")
}

// upsertRoute adds or updates a route in the route table.
func upsertRoute(route Route) {
	routesMu.Lock()
	defer routesMu.Unlock()
	for i, rt := range routes {
		if rt.PluginID == route.PluginID {
			routes[i] = route
			return
		}
	}
	routes = append(routes, route)
}

// evaluateAndNotify checks all routes: if baseURL is set, sends webhook:plugin:url to each.
func evaluateAndNotify(sdkClient *pluginsdk.Client) {
	baseURLMu.RLock()
	url := baseURL
	baseURLMu.RUnlock()
	if url == "" {
		return
	}

	routesMu.RLock()
	snapshot := make([]Route, len(routes))
	copy(snapshot, routes)
	routesMu.RUnlock()

	for _, rt := range snapshot {
		sendPluginURL(sdkClient, rt.PluginID, url, rt.Prefix)
	}
}

// evaluateAndNotifyPlugin sends webhook:plugin:url to a single plugin if baseURL is set.
func evaluateAndNotifyPlugin(sdkClient *pluginsdk.Client, pluginID, prefix string) {
	baseURLMu.RLock()
	url := baseURL
	baseURLMu.RUnlock()
	if url == "" {
		return
	}

	sendPluginURL(sdkClient, pluginID, url, prefix)
}

// sendPluginURL sends an addressed webhook:plugin:url event to a gateway plugin.
func sendPluginURL(sdkClient *pluginsdk.Client, pluginID, tunnelBaseURL, prefix string) {
	webhookURL := strings.TrimRight(tunnelBaseURL, "/") + prefix
	payload := map[string]string{
		"webhook_url": webhookURL,
	}
	data, _ := json.Marshal(payload)

	sdkClient.ReportAddressedEvent("webhook:plugin:url", string(data), pluginID)
	log.Printf("Sent webhook:plugin:url to %s: %s", pluginID, webhookURL)
}

// broadcastWebhookReady broadcasts a webhook:ready event so gateway plugins can register.
func broadcastWebhookReady(sdkClient *pluginsdk.Client, hostname string, port int) {
	payload := map[string]interface{}{
		"host": hostname,
		"port": port,
	}
	data, _ := json.Marshal(payload)
	sdkClient.ReportEvent("webhook:ready", string(data))
	log.Printf("Broadcast webhook:ready (host=%s port=%d)", hostname, port)
}
