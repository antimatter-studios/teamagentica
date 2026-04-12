package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/router"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestRelay creates a relay with an SDK client pointing at the given mock kernel URL.
func newTestRelay(kernelURL string) *relay {
	u, _ := url.Parse(kernelURL)
	host := u.Hostname()
	port := u.Port()

	cfg := pluginsdk.Config{
		KernelHost: host,
		KernelPort: port,
	}

	sdk := pluginsdk.NewClient(cfg, pluginsdk.Registration{
		ID: "infra-agent-relay",
	})
	return newRelay(sdk)
}

// setupRouter creates a gin router with all relay endpoints.
func setupRouter(r *relay) *gin.Engine {
	router := gin.New()
	router.POST("/chat", r.handleChat)
	router.POST("/config/workspace/map", r.handleMapWorkspace)
	router.POST("/config/workspace/unmap", r.handleUnmapWorkspace)
	router.GET("/status", r.handleStatus)
	return router
}

// --- Validation tests (no agent calls needed) ---

func TestHandleChat_MissingFields(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing source_plugin", `{"channel_id":"c1","message":"hi"}`},
		{"missing channel_id", `{"source_plugin":"discord","message":"hi"}`},
		{"missing message", `{"source_plugin":"discord","channel_id":"c1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/chat", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rtr.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleChat_InvalidJSON(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Chat returns 202 with task_group_id for async processing ---

func TestHandleChat_ReturnsTaskGroupID(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-anthropic", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@claude what is Go?"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["task_group_id"] == "" {
		t.Error("expected non-empty task_group_id")
	}
}

// --- Workspace routing is synchronous and takes priority ---

func TestHandleChat_WorkspacePriorityOverAlias(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	r.routes.MapWorkspace("messaging-discord", "c1", "ws-1", "127.0.0.1:99999") // unreachable
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-anthropic", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@claude hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	// Should get a 502 (bridge connect error) not a 202 (async route).
	// This proves workspace takes priority.
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 (workspace route attempted), got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "connect") {
		t.Errorf("expected connect error (workspace route), got %q", resp["error"])
	}
}

// --- Config endpoints ---

func TestMapWorkspaceEndpoint(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","workspace_id":"ws-1","bridge_addr":"10.0.0.1:9999"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/config/workspace/map", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ws := r.routes.GetWorkspace("messaging-discord", "c1")
	if ws == nil || ws.WorkspaceID != "ws-1" {
		t.Errorf("workspace not mapped correctly: %+v", ws)
	}
}

func TestUnmapWorkspaceEndpoint(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	// Map first.
	r.routes.MapWorkspace("messaging-discord", "c1", "ws-1", "addr")

	body := `{"source_plugin":"messaging-discord","channel_id":"c1"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/config/workspace/unmap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ws := r.routes.GetWorkspace("messaging-discord", "c1"); ws != nil {
		t.Error("workspace should have been unmapped")
	}
}

func TestStatusEndpoint(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	r.routes.MapWorkspace("messaging-discord", "c1", "ws-1", "addr")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status", nil)
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &status)

	if _, ok := status["workspace_mappings"]; !ok {
		t.Error("expected 'workspace_mappings' in status")
	}
}

// --- Router table concurrency ---

func TestRouterTable_ConcurrentAccess(t *testing.T) {
	table := router.NewTable()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			table.MapWorkspace("discord", "c1", "ws", "addr")
			table.SetAliases(alias.NewAliasMap(nil))
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		table.GetWorkspace("discord", "c1")
		table.Aliases()
		table.ListWorkspaces()
	}
	<-done
}
