package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newHandlerWithTmpDir creates a test handler with the given settings.
func newHandlerWithTmpDir(apiKey, model, tmpDir string) *Handler {
	return &Handler{
		apiKey:        apiKey,
		model:         model,
		toolLoopLimit: 20,
		client:        gemini.NewClient(apiKey, false),
		usage:         usage.NewTracker(tmpDir),
	}
}

func TestHealthConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	if body["plugin"] != "agent-gemini" {
		t.Errorf("expected plugin=agent-gemini, got %v", body["plugin"])
	}
	if body["configured"] != true {
		t.Errorf("expected configured=true, got %v", body["configured"])
	}
	if body["model"] != "gemini-2.5-flash" {
		t.Errorf("expected model=gemini-2.5-flash, got %v", body["model"])
	}
}

func TestHealthNotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["configured"] != false {
		t.Errorf("expected configured=false when no API key, got %v", body["configured"])
	}
}

func TestChatStreamEmptyMessage(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	req := pluginsdk.AgentChatRequest{}
	ch := h.ChatStream(context.Background(), req)

	var events []pluginsdk.AgentStreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("expected single error event, got %d events", len(events))
	}
}

func TestChatStreamNoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("", "gemini-2.5-flash", tmpDir)

	req := pluginsdk.AgentChatRequest{Message: "hello"}
	ch := h.ChatStream(context.Background(), req)

	var events []pluginsdk.AgentStreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("expected single error event, got %d events", len(events))
	}
}

func TestUsageRecordsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/records", nil)

	h.UsageRecords(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	records, ok := body["records"].([]interface{})
	if !ok {
		t.Fatal("expected records to be an array")
	}
	if len(records) != 0 {
		t.Errorf("expected empty records, got %d", len(records))
	}
}

func TestUsageSummary(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage", nil)

	h.Usage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	for _, key := range []string{"rate_limit", "today", "week", "all_time", "models"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in usage summary", key)
		}
	}
}

func TestModelsNoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil)

	h.Models(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["current"] != "gemini-2.5-flash" {
		t.Errorf("expected current=gemini-2.5-flash, got %v", body["current"])
	}

	models, ok := body["models"].([]interface{})
	if !ok {
		t.Fatal("expected models to be an array")
	}
	if len(models) == 0 {
		t.Error("expected default models list to be non-empty")
	}
}

func TestNewHandlerFromConfig(t *testing.T) {
	h := NewHandler(HandlerConfig{
		APIKey:   "test-key",
		Model:    "gemini-2.5-pro",
		DataPath: t.TempDir(),
	})
	if h.apiKey != "test-key" {
		t.Errorf("expected apiKey=test-key, got %s", h.apiKey)
	}
	if h.model != "gemini-2.5-pro" {
		t.Errorf("expected model=gemini-2.5-pro, got %s", h.model)
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("short string: expected 'hello', got %q", got)
	}
	if got := truncateStr("hello world", 5); got != "hello..." {
		t.Errorf("long string: expected 'hello...', got %q", got)
	}
}

func TestConfigOptionsUnknownField(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/config/options/UNKNOWN_FIELD", nil)
	c.Params = gin.Params{{Key: "field", Value: "UNKNOWN_FIELD"}}

	h.ConfigOptions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["error"] != "Unknown field" {
		t.Errorf("expected 'Unknown field' error, got %v", body["error"])
	}
}
