package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestHandler(apiKey, model, dataPath string) *Handler {
	return NewHandler(HandlerConfig{
		APIKey:   apiKey,
		Model:    model,
		DataPath: dataPath,
	})
}

func TestUsageRecordsEmpty(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/usage/records", nil)

	h.UsageRecords(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	records, ok := resp["records"].([]interface{})
	if !ok {
		t.Fatalf("records not an array: %T", resp["records"])
	}
	if len(records) != 0 {
		t.Errorf("expected empty records, got %d", len(records))
	}
}

func TestUsageSummaryEmpty(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/usage", nil)

	h.Usage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	allTime, ok := resp["all_time"].(map[string]interface{})
	if !ok {
		t.Fatalf("all_time not a map")
	}
	if allTime["requests"] != float64(0) {
		t.Errorf("expected 0 requests, got %v", allTime["requests"])
	}
}

func TestModelsNoAPIKey(t *testing.T) {
	h := newTestHandler("", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/models", nil)

	h.Models(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] != "No API key configured." {
		t.Errorf("expected no-api-key error, got %v", resp["error"])
	}
}

func TestSystemPrompt(t *testing.T) {
	h := NewHandler(HandlerConfig{
		APIKey:        "test",
		Model:         "kimi-k2",
		DataPath:      t.TempDir(),
		DefaultPrompt: "You are Kimi.",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/system-prompt", nil)

	h.SystemPrompt(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["default_prompt"] != "You are Kimi." {
		t.Errorf("expected prompt, got %v", resp["default_prompt"])
	}
}
