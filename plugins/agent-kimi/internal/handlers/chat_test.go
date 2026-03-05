package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-kimi/internal/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestHandler(apiKey, model, dataPath string) *Handler {
	cfg := &config.Config{
		APIKey:   apiKey,
		Model:    model,
		DataPath: dataPath,
	}
	return NewHandler(cfg)
}

func TestHealth(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/health", nil)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp["plugin"] != "agent-kimi" {
		t.Errorf("expected plugin=agent-kimi, got %v", resp["plugin"])
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["configured"] != true {
		t.Errorf("expected configured=true, got %v", resp["configured"])
	}
	if resp["model"] != "kimi-k2-turbo-preview" {
		t.Errorf("expected model=kimi-k2-turbo-preview, got %v", resp["model"])
	}
}

func TestHealthNotConfigured(t *testing.T) {
	h := newTestHandler("", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/health", nil)

	h.Health(c)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["configured"] != false {
		t.Errorf("expected configured=false, got %v", resp["configured"])
	}
}

func TestChatEmptyBody(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/chat", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] != "message or conversation required" {
		t.Errorf("unexpected error: %v", resp["error"])
	}
}

func TestChatEmptyMessage(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":""}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChatInvalidJSON(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/chat", strings.NewReader(`not json`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid request body" {
		t.Errorf("unexpected error: %v", resp["error"])
	}
}

func TestChatNoAPIKey(t *testing.T) {
	h := newTestHandler("", "kimi-k2-turbo-preview", t.TempDir())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/chat", strings.NewReader(`{"message":"hello"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Chat(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	errStr, _ := resp["error"].(string)
	if !strings.Contains(errStr, "KIMI_API_KEY") {
		t.Errorf("expected error mentioning KIMI_API_KEY, got: %v", errStr)
	}
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
