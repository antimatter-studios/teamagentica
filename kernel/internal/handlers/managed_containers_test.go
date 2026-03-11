package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

func newMCTestHandler(t *testing.T) (*PluginHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.ManagedContainer{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return &PluginHandler{db: db, Events: events.NewHub()}, db
}

// --- Input tests: given request data, verify the constructed upstream request ---

// TestProxyRequestConstruction verifies that given a container ID, port, and path,
// buildProxyRequest produces the correct upstream URL and Host header.
// Pure input→output, no network.
func TestProxyRequestConstruction(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		port        int
		path        string
		reqHost     string
		wantURL     string
		wantHost    string
	}{
		{
			name:        "terminal root",
			containerID: "term0001",
			port:        7681,
			path:        "/",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-term0001:7681/ws/term0001/",
			wantHost:    "api.teamagentica.localhost",
		},
		{
			name:        "terminal port forward",
			containerID: "term0001",
			port:        7681,
			path:        "/proxy/5173/",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-term0001:7681/ws/term0001/proxy/5173/",
			wantHost:    "api.teamagentica.localhost",
		},
		{
			name:        "terminal ports API",
			containerID: "term0001",
			port:        7681,
			path:        "/ports",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-term0001:7681/ws/term0001/ports",
			wantHost:    "api.teamagentica.localhost",
		},
		{
			name:        "vscode root",
			containerID: "vscode01",
			port:        8080,
			path:        "/",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-vscode01:8080/ws/vscode01/",
			wantHost:    "api.teamagentica.localhost",
		},
		{
			name:        "vscode static asset",
			containerID: "vscode01",
			port:        8080,
			path:        "/static/browser/workbench.js",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-vscode01:8080/ws/vscode01/static/browser/workbench.js",
			wantHost:    "api.teamagentica.localhost",
		},
		{
			name:        "vscode websocket",
			containerID: "vscode01",
			port:        8080,
			path:        "/stable-abc123",
			reqHost:     "api.teamagentica.localhost",
			wantURL:     "http://teamagentica-mc-vscode01:8080/ws/vscode01/stable-abc123",
			wantHost:    "api.teamagentica.localhost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &models.ManagedContainer{ID: tt.containerID, Port: tt.port}
			incoming := httptest.NewRequest("GET", "/ws/"+tt.containerID+tt.path, nil)
			incoming.Host = tt.reqHost

			got := buildProxyRequest(mc, tt.path, incoming)

			if gotURL := got.URL.String(); gotURL != tt.wantURL {
				t.Errorf("URL:\n  got:  %s\n  want: %s", gotURL, tt.wantURL)
			}
			if got.Host != tt.wantHost {
				t.Errorf("Host:\n  got:  %s\n  want: %s", got.Host, tt.wantHost)
			}
		})
	}
}

// TestContainerTargetURL verifies the URL format for container hostnames.
func TestContainerTargetURL(t *testing.T) {
	tests := []struct {
		id   string
		port int
		want string
	}{
		{"abc12345", 7681, "http://teamagentica-mc-abc12345:7681"},
		{"vscode01", 8080, "http://teamagentica-mc-vscode01:8080"},
	}
	for _, tt := range tests {
		mc := &models.ManagedContainer{ID: tt.id, Port: tt.port}
		if got := containerTargetURL(mc); got != tt.want {
			t.Errorf("containerTargetURL(%s, %d) = %q, want %q", tt.id, tt.port, got, tt.want)
		}
	}
}

// --- Output tests: given a response from the backend, verify what the proxy returns ---

// TestProxyContainerNotFound verifies the handler returns 404 JSON for unknown IDs.
func TestProxyContainerNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newMCTestHandler(t)

	engine := gin.New()
	engine.Any("/ws/:container_id/*path", h.ProxyToManagedContainer)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ws/nonexist/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestProxyContainerNotRunning verifies the handler returns 503 for stopped containers.
func TestProxyContainerNotRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, db := newMCTestHandler(t)
	db.Create(&models.ManagedContainer{
		ID: "stopped1", PluginID: "test", ContainerID: "", Status: "stopped",
		Port: 8080, Subdomain: "ws-stopped1",
	})

	engine := gin.New()
	engine.Any("/ws/:container_id/*path", h.ProxyToManagedContainer)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ws/stopped1/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestProxyStripsCSP verifies that when the backend sends a CSP header,
// the proxy strips it from the response (needed for iframe embedding).
func TestProxyStripsCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	testContainerTargetOverride = backend.URL
	defer func() { testContainerTargetOverride = "" }()

	h, db := newMCTestHandler(t)
	db.Create(&models.ManagedContainer{
		ID: "csp00001", PluginID: "test", ContainerID: "docker-c1", Status: "running",
		Port: 8080, Subdomain: "ws-csp00001",
	})

	engine := gin.New()
	engine.Any("/ws/:container_id/*path", h.ProxyToManagedContainer)
	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ws/csp00001/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
		t.Errorf("CSP header should be stripped, got %q", csp)
	}
}
