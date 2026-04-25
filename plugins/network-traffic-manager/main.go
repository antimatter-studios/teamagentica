package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
	_ "github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers/ngrok"
	_ "github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers/sshjumphost"
	_ "github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers/sshreverse"
	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/manager"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	pluginManifest := pluginsdk.LoadManifest()

	const defaultPort = 9100
	mgr := manager.New()

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginManifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: pluginManifest.Capabilities,
		Version:      pluginsdk.DevVersion(pluginManifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": getConfigSchema(),
				"status": statusSnapshot(mgr),
			}
		},
	})

	mgr.OnReady(func(name, role, url string) {
		log.Printf("[tunnel %q] ready: role=%q url=%s", name, role, url)
		if role == manager.RoleIngress && url != "" {
			events.PublishIngressReady(sdkClient, url)
			log.Printf("broadcast ingress:ready url=%s (from tunnel %q)", url, name)
		}
	})

	sdkClient.Events().On("webhook:ready", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var data struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("[webhook] parse webhook:ready: %v", err)
			return
		}
		if data.Host == "" || data.Port == 0 {
			return
		}
		target := fmt.Sprintf("%s:%d", data.Host, data.Port)
		if !mgr.SetWebhookTarget(target) {
			return
		}
		log.Printf("[webhook] discovered webhook plugin at %s", target)
		mgr.StartTunnelsTargetingWebhook(context.Background())
	}))

	sdkClient.Events().On("ingress:request", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		for _, t := range mgr.List() {
			if t.Spec.Role == manager.RoleIngress && t.Status.URL != "" {
				events.PublishIngressReady(sdkClient, t.Status.URL)
				log.Printf("rebroadcast ingress:ready url=%s", t.Status.URL)
				return
			}
		}
	}))

	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		applyConfig(context.Background(), mgr, p.Config)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("fetch plugin config: %v", err)
	}

	httpPort := defaultPort
	if v := pluginConfig["HTTP_PORT"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			httpPort = p
		}
	}

	applyConfig(ctx, mgr, pluginConfig)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"tunnels": mgr.List(),
		})
	})
	mux.HandleFunc("GET /tunnels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, mgr.List())
	})
	mux.HandleFunc("GET /tunnels/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		t, ok := mgr.Get(name)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, t)
	})
	mux.HandleFunc("POST /tunnels", func(w http.ResponseWriter, r *http.Request) {
		var spec manager.Spec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		view, err := mgr.AddOrReplace(r.Context(), spec)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("DELETE /tunnels/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := mgr.Remove(r.Context(), name); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"state": "removed"})
	})
	mux.HandleFunc("POST /tunnels/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		st, err := mgr.Start(r.Context(), name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /tunnels/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := mgr.Stop(r.Context(), name); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"state": drivers.StateStopped})
	})

	sdkClient.ListenAndServe(httpPort, mux)

	mgr.StopAll(context.Background())
	log.Println("network-traffic-manager shut down")
}

func applyConfig(ctx context.Context, mgr *manager.Manager, cfg map[string]string) {
	specs, err := manager.ParseSpecs(cfg["TUNNELS"])
	if err != nil {
		log.Printf("[config] %v — ignoring TUNNELS", err)
		return
	}
	mgr.ApplySpecs(ctx, specs)
	log.Printf("[config] applied %d tunnel spec(s)", len(specs))
}

func statusSnapshot(mgr *manager.Manager) map[string]string {
	out := map[string]string{}
	for _, t := range mgr.List() {
		v := t.Status.State
		if t.Status.URL != "" {
			v += " " + t.Status.URL
		}
		if t.Status.Error != "" {
			v += " (" + t.Status.Error + ")"
		}
		out[t.Spec.Name] = v
	}
	if len(out) == 0 {
		out["tunnels"] = "(none configured)"
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func getConfigSchema() map[string]pluginsdk.ConfigSchemaField {
	return map[string]pluginsdk.ConfigSchemaField{
		"TUNNELS": {
			Type:     "tunnels",
			Label:    "Tunnels",
			HelpText: "Named tunnels managed by this plugin. Add a tunnel, pick a driver (ngrok or ssh-reverse), and fill in its config.",
		},
		"HTTP_PORT": {
			Type:     "number",
			Label:    "HTTP Port",
			Default:  "9100",
			HelpText: "Port for the health + control endpoints",
		},
	}
}
