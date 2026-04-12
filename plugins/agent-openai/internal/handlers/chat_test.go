package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/usage"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newHandlerWithTmpDir creates a handler using the given temp dir for usage tracking.
func newHandlerWithTmpDir(backend, apiKey, model, tmpDir string) *Handler {
	return &Handler{
		backend: backend,
		apiKey:  apiKey,
		model:   model,
		usage:   usage.NewTracker(tmpDir),
	}
}

func TestUsageRecordsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)

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
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage", nil)

	h.Usage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	// Should have top-level keys: rate_limit, today, week, all_time, models
	for _, key := range []string{"rate_limit", "today", "week", "all_time", "models"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in usage summary", key)
		}
	}
}

func TestModelsDefaultBackend(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("subscription", "", "gpt-5.3-codex", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil)

	h.Models(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["current"] != "gpt-5.3-codex" {
		t.Errorf("expected current=gpt-5.3-codex, got %v", body["current"])
	}
}

func TestNewHandlerFromConfig(t *testing.T) {
	h := NewHandler(HandlerConfig{
		Backend:  "api_key",
		APIKey:   "sk-test",
		Model:    "gpt-4o",
		Endpoint: "https://api.openai.com/v1",
		DataPath: t.TempDir(),
	})
	if h.backend != "api_key" {
		t.Errorf("expected backend=api_key, got %s", h.backend)
	}
	if h.apiKey != "sk-test" {
		t.Errorf("expected apiKey=sk-test, got %s", h.apiKey)
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

func TestAuthStatusNoCLI(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("subscription", "", "gpt-5.3-codex", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/auth/status", nil)

	h.AuthStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["codex_enabled"] != false {
		t.Errorf("expected codex_enabled=false, got %v", body["codex_enabled"])
	}
}

func TestTracker(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)
	if h.Tracker() == nil {
		t.Error("expected non-nil tracker")
	}
}
