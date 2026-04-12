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
	return NewHandler(apiKey, model, dataPath, false)
}

func TestUsageRecordsEmpty(t *testing.T) {
	h := newTestHandler("test-key", "google/gemini-2.5-flash", t.TempDir())

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
	h := newTestHandler("test-key", "google/gemini-2.5-flash", t.TempDir())

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

func TestTruncateStr(t *testing.T) {
	short := "hello"
	if got := truncateStr(short, 10); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}

	long := "abcdefghij"
	if got := truncateStr(long, 5); got != "abcde..." {
		t.Errorf("expected abcde..., got %s", got)
	}
}

func TestModelsNoAPIKey(t *testing.T) {
	h := newTestHandler("", "google/gemini-2.5-flash", t.TempDir())

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
		t.Errorf("expected error about API key, got %v", resp["error"])
	}
}

func TestConfigOptionsUnknownField(t *testing.T) {
	h := newTestHandler("test-key", "google/gemini-2.5-flash", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/config/options/UNKNOWN", nil)
	c.Params = gin.Params{{Key: "field", Value: "UNKNOWN"}}

	h.ConfigOptions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] != "Unknown field" {
		t.Errorf("expected 'Unknown field' error, got %v", resp["error"])
	}
}
