package pluginsdk

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
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
		KernelHost: u.Hostname(),
		KernelPort: u.Port(),
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
	cfg := Config{KernelHost: "10.0.0.1", KernelPort: "8080"}
	c := NewClient(cfg, Registration{})
	got := c.kernelURL()
	want := "http://10.0.0.1:8080"
	if got != want {
		t.Fatalf("kernelURL() = %q, want %q", got, want)
	}
}

func TestKernelURL_HTTPS(t *testing.T) {
	cfg := Config{KernelHost: "kernel.local", KernelPort: "443", TLSCert: "/cert.pem"}
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
		"TEAMAGENTICA_KERNEL_HOST": "myhost",
		"TEAMAGENTICA_KERNEL_PORT": "9999",
		"TEAMAGENTICA_TLS_CERT":    "/cert.pem",
		"TEAMAGENTICA_TLS_KEY":     "/key.pem",
		"TEAMAGENTICA_TLS_CA":      "/ca.pem",
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
	if cfg.TLSCert != "/cert.pem" {
		t.Errorf("TLSCert = %q, want %q", cfg.TLSCert, "/cert.pem")
	}
	if cfg.TLSKey != "/key.pem" {
		t.Errorf("TLSKey = %q, want %q", cfg.TLSKey, "/key.pem")
	}
	if cfg.TLSCA != "/ca.pem" {
		t.Errorf("TLSCA = %q, want %q", cfg.TLSCA, "/ca.pem")
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

// --- PublishEvent ---

func TestPublishEvent(t *testing.T) {
	var captured []byte
	var capturedPath string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	client, srv := testEnv(t, handler)

	// Inject infra-redis peer so PublishEvent can route to it.
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	client.SetPeer("infra-redis", u.Hostname(), port)

	client.PublishEvent("debug:info", "something happened")

	if capturedPath != "/events/publish" {
		t.Errorf("path = %q, want /events/publish", capturedPath)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["source"] != "test-plugin" {
		t.Errorf("source = %q, want test-plugin", payload["source"])
	}
	if payload["event_type"] != "debug:info" {
		t.Errorf("event_type = %q, want debug:info", payload["event_type"])
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

	client, srv := testEnv(t, handler)

	// Inject infra-redis peer so PublishEventTo can route to it.
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	client.SetPeer("infra-redis", u.Hostname(), port)

	client.ReportUsage(UsageReport{
		Provider:     "anthropic",
		Model:        "claude-4",
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		DurationMs:   500,
	})

	var payload map[string]interface{}
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Must have addressed delivery to infra-cost-tracking.
	if payload["target"] != "infra-cost-tracking" {
		t.Errorf("target = %q, want infra-cost-tracking", payload["target"])
	}
	if payload["event_type"] != "usage:report" {
		t.Errorf("event_type = %q, want usage:report", payload["event_type"])
	}
	if payload["source"] != "test-plugin" {
		t.Errorf("source = %q, want test-plugin", payload["source"])
	}

	// detail should be a marshaled UsageReport.
	detail, _ := payload["detail"].(string)
	var report UsageReport
	if err := json.Unmarshal([]byte(detail), &report); err != nil {
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
		TLSCert: "/nonexistent/cert.pem",
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
	cfg := Config{}
	tlsCfg, err := GetServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg != nil {
		t.Error("expected nil TLS config when TLS is disabled")
	}
}

func TestGetServerTLSConfig_MissingFields(t *testing.T) {
	// Missing cert paths — should return nil, nil.
	cfg := Config{TLSCert: "", TLSKey: "", TLSCA: ""}
	tlsCfg, err := GetServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg != nil {
		t.Error("expected nil when cert fields are empty")
	}
}

