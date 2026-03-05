package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/config"
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

func TestHealthAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)

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
	if body["plugin"] != "agent-openai" {
		t.Errorf("expected plugin=agent-openai, got %v", body["plugin"])
	}
	if body["configured"] != true {
		t.Errorf("expected configured=true, got %v", body["configured"])
	}
	if body["backend"] != "api_key" {
		t.Errorf("expected backend=api_key, got %v", body["backend"])
	}
}

func TestHealthAPIKeyNotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "", "gpt-4o", tmpDir)

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

func TestHealthSubscriptionNoCLI(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("subscription", "", "gpt-5.3-codex", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["configured"] != false {
		t.Errorf("expected configured=false when codexCLI is nil, got %v", body["configured"])
	}
	if body["backend"] != "subscription" {
		t.Errorf("expected backend=subscription, got %v", body["backend"])
	}
}

func TestChatEmptyMessage(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)

	reqBody := `{"message":"","conversation":[]}`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["error"] != "message or conversation required" {
		t.Errorf("expected error about empty message, got %v", body["error"])
	}
}

func TestChatInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{bad json`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChatSubscriptionNoCLI(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("subscription", "", "gpt-5.3-codex", tmpDir)

	reqBody := `{"message":"hello"}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when codexCLI nil, got %d", w.Code)
	}
}

func TestChatAPIKeyNotSet(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "", "gpt-4o", tmpDir)

	reqBody := `{"message":"hello"}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when api key empty, got %d", w.Code)
	}
}

func TestChatUnknownBackend(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("bogus", "", "gpt-4o", tmpDir)

	reqBody := `{"message":"hello"}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown backend, got %d", w.Code)
	}
}

func TestEmitUsageNoSDK(t *testing.T) {
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("api_key", "sk-test", "gpt-4o", tmpDir)
	// sdk is nil — should not panic
	h.emitUsage("openai", "gpt-4o", 100, 50, 150, 0, 1000, "")
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
	cfg := &config.Config{
		Backend:       "api_key",
		OpenAIAPIKey:  "sk-test",
		OpenAIModel:   "gpt-4o",
		OpenAIEndpoint: "https://api.openai.com/v1",
		CodexDataPath: t.TempDir(),
	}

	h := NewHandler(cfg)
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
