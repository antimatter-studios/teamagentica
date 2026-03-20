package pluginsdk

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// testEnv is a helper that creates a Client pointing at a httptest.Server.
// The server records every request it receives into the returned slice pointer.
func testEnv(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	cfg := Config{
		KernelHost:  u.Hostname(),
		KernelPort:  u.Port(),
		PluginToken: "tok-abc",
		TLSEnabled:  false,
	}
	reg := Registration{
		ID:           "test-plugin",
		Host:         "localhost",
		Port:         9000,
		Capabilities: []string{"chat"},
		Version:      "0.1.0",
	}
	return NewClient(cfg, reg), srv
}

// --- kernelURL ---

func TestKernelURL_HTTP(t *testing.T) {
	cfg := Config{KernelHost: "10.0.0.1", KernelPort: "8080", TLSEnabled: false}
	c := NewClient(cfg, Registration{})
	got := c.kernelURL()
	want := "http://10.0.0.1:8080"
	if got != want {
		t.Fatalf("kernelURL() = %q, want %q", got, want)
	}
}

func TestKernelURL_HTTPS(t *testing.T) {
	cfg := Config{KernelHost: "kernel.local", KernelPort: "443", TLSEnabled: true}
	c := NewClient(cfg, Registration{})
	got := c.kernelURL()
	want := "https://kernel.local:443"
	if got != want {
		t.Fatalf("kernelURL() = %q, want %q", got, want)
	}
}

// --- LoadConfig ---

func TestLoadConfig(t *testing.T) {
	envs := map[string]string{
		"TEAMAGENTICA_KERNEL_HOST":  "myhost",
		"TEAMAGENTICA_KERNEL_PORT":  "9999",
		"TEAMAGENTICA_PLUGIN_TOKEN": "secret",
		"TEAMAGENTICA_TLS_CERT":     "/cert.pem",
		"TEAMAGENTICA_TLS_KEY":      "/key.pem",
		"TEAMAGENTICA_TLS_CA":       "/ca.pem",
		"TEAMAGENTICA_TLS_ENABLED":  "true",
	}
	for k, v := range envs {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	cfg := LoadConfig()

	if cfg.KernelHost != "myhost" {
		t.Errorf("KernelHost = %q, want %q", cfg.KernelHost, "myhost")
	}
	if cfg.KernelPort != "9999" {
		t.Errorf("KernelPort = %q, want %q", cfg.KernelPort, "9999")
	}
	if cfg.PluginToken != "secret" {
		t.Errorf("PluginToken = %q, want %q", cfg.PluginToken, "secret")
	}
	if cfg.TLSCert != "/cert.pem" {
		t.Errorf("TLSCert = %q, want %q", cfg.TLSCert, "/cert.pem")
	}
	if cfg.TLSKey != "/key.pem" {
		t.Errorf("TLSKey = %q, want %q", cfg.TLSKey, "/key.pem")
	}
	if cfg.TLSCA != "/ca.pem" {
		t.Errorf("TLSCA = %q, want %q", cfg.TLSCA, "/ca.pem")
	}
	if !cfg.TLSEnabled {
		t.Errorf("TLSEnabled = false, want true")
	}
}

func TestLoadConfig_TLSDisabledWhenNotTrue(t *testing.T) {
	os.Setenv("TEAMAGENTICA_TLS_ENABLED", "false")
	defer os.Unsetenv("TEAMAGENTICA_TLS_ENABLED")

	cfg := LoadConfig()
	if cfg.TLSEnabled {
		t.Errorf("TLSEnabled = true when env is 'false', want false")
	}
}

// --- UsageReport marshaling ---

func TestUsageReport_JSONFields(t *testing.T) {
	r := UsageReport{
		Provider:     "anthropic",
		Model:        "claude-4",
		RecordType:   "llm_call",
		Status:       "success",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CachedTokens: 20,
		DurationMs:   1234,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	checks := map[string]interface{}{
		"provider":      "anthropic",
		"model":         "claude-4",
		"record_type":   "llm_call",
		"status":        "success",
		"input_tokens":  float64(100),
		"output_tokens": float64(50),
		"total_tokens":  float64(150),
		"cached_tokens": float64(20),
		"duration_ms":   float64(1234),
	}
	for key, want := range checks {
		got, ok := m[key]
		if !ok {
			t.Errorf("missing JSON key %q", key)
			continue
		}
		if got != want {
			t.Errorf("key %q = %v, want %v", key, got, want)
		}
	}
}

func TestUsageReport_OmitEmpty(t *testing.T) {
	r := UsageReport{Provider: "openai"}
	data, _ := json.Marshal(r)

	var m map[string]interface{}
	json.Unmarshal(data, &m)

	// Only "provider" should be present; everything else is omitempty.
	if len(m) != 1 {
		t.Errorf("expected 1 field, got %d: %v", len(m), m)
	}
	if m["provider"] != "openai" {
		t.Errorf("provider = %v, want openai", m["provider"])
	}
}

// --- ReportEvent ---

func TestReportEvent(t *testing.T) {
	var captured []byte
	var capturedPath string
	var capturedAuth string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	client.ReportEvent("debug:info", "something happened")

	if capturedPath != "/api/plugins/event" {
		t.Errorf("path = %q, want /api/plugins/event", capturedPath)
	}
	if capturedAuth != "Bearer tok-abc" {
		t.Errorf("auth = %q, want Bearer tok-abc", capturedAuth)
	}

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["id"] != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", payload["id"])
	}
	if payload["type"] != "debug:info" {
		t.Errorf("type = %q, want debug:info", payload["type"])
	}
	if payload["detail"] != "something happened" {
		t.Errorf("detail = %q, want 'something happened'", payload["detail"])
	}
}

// --- ReportUsage ---

func TestReportUsage(t *testing.T) {
	var captured []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	client.ReportUsage(UsageReport{
		Provider:     "anthropic",
		Model:        "claude-4",
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		DurationMs:   500,
	})

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Must have addressed delivery to infra-cost-tracking.
	if payload["destination"] != "infra-cost-tracking" {
		t.Errorf("destination = %q, want infra-cost-tracking", payload["destination"])
	}
	if payload["type"] != "usage:report" {
		t.Errorf("type = %q, want usage:report", payload["type"])
	}
	if payload["id"] != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", payload["id"])
	}

	// detail should be a marshaled UsageReport.
	var report UsageReport
	if err := json.Unmarshal([]byte(payload["detail"]), &report); err != nil {
		t.Fatalf("unmarshal detail into UsageReport: %v", err)
	}
	if report.Provider != "anthropic" {
		t.Errorf("report.Provider = %q, want anthropic", report.Provider)
	}
	if report.InputTokens != 200 {
		t.Errorf("report.InputTokens = %d, want 200", report.InputTokens)
	}
	if report.DurationMs != 500 {
		t.Errorf("report.DurationMs = %d, want 500", report.DurationMs)
	}
}

// --- Subscribe ---

func TestSubscribe(t *testing.T) {
	var captured []byte
	var capturedPath string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	err := client.Subscribe("chat:message", "/callbacks/chat")
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}

	if capturedPath != "/api/plugins/subscribe" {
		t.Errorf("path = %q, want /api/plugins/subscribe", capturedPath)
	}

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["id"] != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", payload["id"])
	}
	if payload["event_type"] != "chat:message" {
		t.Errorf("event_type = %q, want chat:message", payload["event_type"])
	}
	if payload["callback_path"] != "/callbacks/chat" {
		t.Errorf("callback_path = %q, want /callbacks/chat", payload["callback_path"])
	}
}

func TestSubscribe_KernelError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	client, _ := testEnv(t, handler)
	err := client.Subscribe("chat:message", "/cb")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500: %v", err)
	}
}

// --- Unsubscribe ---

func TestUnsubscribe(t *testing.T) {
	var captured []byte
	var capturedPath string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	err := client.Unsubscribe("chat:message")
	if err != nil {
		t.Fatalf("Unsubscribe returned error: %v", err)
	}

	if capturedPath != "/api/plugins/unsubscribe" {
		t.Errorf("path = %q, want /api/plugins/unsubscribe", capturedPath)
	}

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["id"] != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", payload["id"])
	}
	if payload["event_type"] != "chat:message" {
		t.Errorf("event_type = %q, want chat:message", payload["event_type"])
	}
}

func TestUnsubscribe_KernelError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	client, _ := testEnv(t, handler)
	err := client.Unsubscribe("bogus")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400: %v", err)
	}
}

// --- Stop (deregister) ---

func TestStop_Deregister(t *testing.T) {
	var capturedPath string
	var captured []byte

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	client.Stop()

	if capturedPath != "/api/plugins/deregister" {
		t.Errorf("path = %q, want /api/plugins/deregister", capturedPath)
	}

	var payload map[string]string
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["id"] != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", payload["id"])
	}
}

// --- NewClient without TLS ---

func TestNewClient_NoTLS(t *testing.T) {
	cfg := Config{KernelHost: "localhost", KernelPort: "8080"}
	reg := Registration{ID: "p1"}
	c := NewClient(cfg, reg)

	if c.TLSConfig() != nil {
		t.Error("TLSConfig() should be nil when TLS is not enabled")
	}
}

// --- NewClient with invalid TLS paths (should fall back) ---

func TestNewClient_BadTLSPaths(t *testing.T) {
	cfg := Config{
		KernelHost: "localhost",
		KernelPort: "8080",
		TLSEnabled: true,
		TLSCert:    "/nonexistent/cert.pem",
		TLSKey:     "/nonexistent/key.pem",
		TLSCA:      "/nonexistent/ca.pem",
	}
	reg := Registration{ID: "p1"}
	c := NewClient(cfg, reg)

	// Should fall back to no TLS on the HTTP client.
	if c.TLSConfig() != nil {
		t.Error("TLSConfig() should be nil when cert files don't exist")
	}
}

// --- GetServerTLSConfig with TLS disabled ---

func TestGetServerTLSConfig_Disabled(t *testing.T) {
	cfg := Config{TLSEnabled: false}
	tlsCfg, err := GetServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg != nil {
		t.Error("expected nil TLS config when TLS is disabled")
	}
}

func TestGetServerTLSConfig_MissingFields(t *testing.T) {
	// TLS enabled but missing cert paths — should return nil, nil.
	cfg := Config{TLSEnabled: true, TLSCert: "", TLSKey: "", TLSCA: ""}
	tlsCfg, err := GetServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg != nil {
		t.Error("expected nil when cert fields are empty")
	}
}

// --- Authorization header ---

func TestAuthorizationHeader(t *testing.T) {
	var capturedAuth string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	client, _ := testEnv(t, handler)
	_ = client.Subscribe("x", "/cb")

	if capturedAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q, want 'Bearer tok-abc'", capturedAuth)
	}
}
