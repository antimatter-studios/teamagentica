package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-google/internal/gemini"
	"github.com/antimatter-studios/teamagentica/plugins/agent-google/internal/usage"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newHandlerWithTmpDir creates a test handler with the given settings.
func newHandlerWithTmpDir(apiKey, model, tmpDir string) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		client: gemini.NewClient(apiKey, false),
		usage:  usage.NewTracker(tmpDir),
	}
}

func TestHealthConfigured(t *testing.T) {
	// Health is now handled by agentkit, but we can still test the handler's Models endpoint.
	tmpDir := t.TempDir()
	h := newHandlerWithTmpDir("test-key", "gemini-2.5-flash", tmpDir)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil)

	h.Models(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	if body["current"] != "gemini-2.5-flash" {
		t.Errorf("expected current=gemini-2.5-flash, got %v", body["current"])
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
