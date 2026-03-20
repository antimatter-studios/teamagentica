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
		KernelHost:  host,
		KernelPort:  port,
		PluginToken: "test-token",
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
	router.POST("/config/coordinator", r.handleSetCoordinator)
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

func TestHandleChat_NoCoordinator(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "no coordinator") {
		t.Errorf("expected 'no coordinator' error, got %q", resp["error"])
	}
}

// --- Alias routing tests ---

func TestHandleChat_AliasEmptyRemainder(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	// Set up alias.
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@claude"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Response, "Usage:") {
		t.Errorf("expected usage hint, got %q", resp.Response)
	}
	if resp.Responder != "claude" {
		t.Errorf("expected responder=claude, got %q", resp.Responder)
	}
}

// --- Agent routing via mock kernel ---

func TestHandleChat_AliasRoutesToAgent(t *testing.T) {
	// Mock kernel that responds to /api/route/agent-claude/chat.
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/route/agent-claude/chat" {
			json.NewEncoder(w).Encode(agentChatResponse{Response: "Hello from Claude!"})
			return
		}
		http.NotFound(w, r)
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@claude what is Go?"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response != "Hello from Claude!" {
		t.Errorf("expected 'Hello from Claude!', got %q", resp.Response)
	}
	if resp.Responder != "claude" {
		t.Errorf("expected responder=claude, got %q", resp.Responder)
	}
}

func TestHandleChat_CoordinatorRoutesToAgent(t *testing.T) {
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/route/agent-claude/chat" {
			json.NewEncoder(w).Encode(agentChatResponse{Response: "Coordinator response"})
			return
		}
		http.NotFound(w, r)
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	r.routes.SetCoordinator("messaging-discord", "agent-claude", "claude-sonnet-4-6")

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response != "Coordinator response" {
		t.Errorf("expected 'Coordinator response', got %q", resp.Response)
	}
}

func TestHandleChat_CoordinatorDelegatesToAlias(t *testing.T) {
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/route/agent-claude/chat":
			// Coordinator returns a JSON DAG plan delegating to @codex.
			plan := `{"tasks":[{"id":"t1","alias":"codex","prompt":"fix the bug","depends_on":[]}]}`
			json.NewEncoder(w).Encode(agentChatResponse{Response: plan})
		case "/api/route/agent-openai/chat":
			// Codex handles the delegated task.
			json.NewEncoder(w).Encode(agentChatResponse{Response: "Bug fixed!"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	r.routes.SetCoordinator("messaging-discord", "agent-claude", "")
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"please fix the bug"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response != "Bug fixed!" {
		t.Errorf("expected 'Bug fixed!', got %q", resp.Response)
	}
	if resp.Responder != "codex" {
		t.Errorf("expected responder=codex, got %q", resp.Responder)
	}
}

func TestHandleChat_AgentError(t *testing.T) {
	// Kernel returns 500.
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"agent crashed"}`))
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	r.routes.SetCoordinator("messaging-discord", "agent-claude", "")

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Alias takes priority over coordinator ---

func TestHandleChat_AliasPriorityOverCoordinator(t *testing.T) {
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/route/agent-openai/chat":
			json.NewEncoder(w).Encode(agentChatResponse{Response: "Direct alias response"})
		case "/api/route/agent-claude/chat":
			json.NewEncoder(w).Encode(agentChatResponse{Response: "Coordinator response"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	// Both coordinator and alias configured.
	r.routes.SetCoordinator("messaging-discord", "agent-claude", "")
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"agent"}},
	}))

	// Message starts with @codex — should route directly, skipping coordinator.
	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@codex hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response != "Direct alias response" {
		t.Errorf("expected alias route, got coordinator: %q", resp.Response)
	}
}

// --- Config endpoints ---

func TestSetCoordinatorEndpoint(t *testing.T) {
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	body := `{"source_plugin":"messaging-discord","plugin_id":"agent-claude","model":"claude-sonnet-4-6"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/config/coordinator", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	coord := r.routes.GetCoordinator("messaging-discord")
	if coord == nil || coord.PluginID != "agent-claude" {
		t.Errorf("coordinator not set correctly: %+v", coord)
	}
}

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

	r.routes.SetCoordinator("messaging-discord", "agent-claude", "")
	r.routes.MapWorkspace("messaging-discord", "c1", "ws-1", "addr")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/status", nil)
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &status)

	if _, ok := status["coordinators"]; !ok {
		t.Error("expected 'coordinators' in status")
	}
	if _, ok := status["workspace_mappings"]; !ok {
		t.Error("expected 'workspace_mappings' in status")
	}
}

// --- Workspace routing priority ---

func TestHandleChat_WorkspacePriorityOverAlias(t *testing.T) {
	// If a channel is mapped to a workspace, it should route there
	// even if the message contains @alias.
	// We can't test actual TCP bridge connection, but we can verify
	// the workspace check happens first by observing the error type.
	r := newTestRelay("http://localhost:0")
	rtr := setupRouter(r)

	r.routes.MapWorkspace("messaging-discord", "c1", "ws-1", "127.0.0.1:99999") // unreachable
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
	}))

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"@claude hello"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	// Should get a 502 (bridge connect error) not a 200 (alias route).
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

// --- Routing table interaction: workspace, alias, coordinator ---

func TestHandleChat_FullRoutingOrder(t *testing.T) {
	// No workspace mapped, no alias match → falls through to coordinator.
	kernel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(agentChatResponse{Response: "coordinator handled it"})
	}))
	defer kernel.Close()

	r := newTestRelay(kernel.URL)
	rtr := setupRouter(r)

	r.routes.SetCoordinator("messaging-discord", "agent-claude", "")
	r.routes.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"agent"}},
	}))
	// Only codex alias — message without @codex should fall through to coordinator.

	body := `{"source_plugin":"messaging-discord","channel_id":"c1","message":"hello world"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rtr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp relayResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Response != "coordinator handled it" {
		t.Errorf("expected coordinator response, got %q", resp.Response)
	}
}

// --- Router table concurrency ---

func TestRouterTable_ConcurrentAccess(t *testing.T) {
	table := router.NewTable()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			table.SetCoordinator("discord", "agent-a", "model")
			table.MapWorkspace("discord", "c1", "ws", "addr")
			table.SetAliases(alias.NewAliasMap(nil))
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		table.GetCoordinator("discord")
		table.GetWorkspace("discord", "c1")
		table.Aliases()
		table.ListCoordinators()
		table.ListWorkspaces()
	}
	<-done
}
