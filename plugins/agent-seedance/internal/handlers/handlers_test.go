package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-seedance/internal/usage"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestTracker(t *testing.T) *usage.Tracker {
	return usage.NewTracker(t.TempDir())
}

func newTestHandler(apiKey string) *Handler {
	return NewHandler(apiKey, "/tmp/test-seedance", false)
}

func TestHealth(t *testing.T) {
	h := newTestHandler("test-key")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if resp["plugin"] != "agent-seedance" {
		t.Errorf("expected plugin=agent-seedance, got %v", resp["plugin"])
	}
	if resp["version"] != "1.0.0" {
		t.Errorf("expected version=1.0.0, got %v", resp["version"])
	}
	if resp["configured"] != true {
		t.Errorf("expected configured=true, got %v", resp["configured"])
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}

func TestHealthNoAPIKey(t *testing.T) {
	h := newTestHandler("")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	h.Health(c)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["configured"] != false {
		t.Errorf("expected configured=false when no API key, got %v", resp["configured"])
	}
}

func TestGenerateNoAPIKey(t *testing.T) {
	h := newTestHandler("")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	body := `{"prompt": "ocean waves"}`
	c.Request, _ = http.NewRequest("POST", "/generate", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	errMsg, ok := resp["error"].(string)
	if !ok || errMsg == "" {
		t.Errorf("expected error message about API key, got %v", resp["error"])
	}
}

func TestGenerateMissingPrompt(t *testing.T) {
	h := newTestHandler("test-key")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	c.Request, _ = http.NewRequest("POST", "/generate", bytes.NewBufferString(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	errMsg, ok := resp["error"].(string)
	if !ok || errMsg == "" {
		t.Errorf("expected error about prompt, got %v", resp["error"])
	}
}

func TestGenerateEmptyBody(t *testing.T) {
	h := newTestHandler("test-key")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	c.Request, _ = http.NewRequest("POST", "/generate", bytes.NewBufferString(``))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestConfigOptionsModel(t *testing.T) {
	h := newTestHandler("")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "field", Value: "SEEDANCE_MODEL"}}

	h.ConfigOptions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	options, ok := resp["options"].([]interface{})
	if !ok || len(options) != 1 {
		t.Errorf("expected 1 model option, got %v", resp["options"])
	}
	if options[0] != "seedance-2.0" {
		t.Errorf("expected seedance-2.0, got %v", options[0])
	}
}

func TestConfigOptionsUnknownField(t *testing.T) {
	h := newTestHandler("")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "field", Value: "UNKNOWN"}}

	h.ConfigOptions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	options, ok := resp["options"].([]interface{})
	if !ok || len(options) != 0 {
		t.Errorf("expected empty options for unknown field, got %v", resp["options"])
	}
}

func TestUsageRecordsEmpty(t *testing.T) {
	h := newTestHandler("test-key")
	h.usage = newTestTracker(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/usage/records", nil)

	h.UsageRecords(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	records, ok := resp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected records array, got %T", resp["records"])
	}
	if len(records) != 0 {
		t.Errorf("expected empty records, got %d", len(records))
	}
}

func TestStatusNotFound(t *testing.T) {
	h := newTestHandler("test-key")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/status/nonexistent", nil)
	c.Params = gin.Params{{Key: "taskId", Value: "nonexistent"}}

	h.Status(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTruncateStr(t *testing.T) {
	short := "hello"
	if got := truncateStr(short, 10); got != "hello" {
		t.Errorf("expected 'hello', got '%s'", got)
	}

	long := "abcdefghij"
	if got := truncateStr(long, 5); got != "abcde..." {
		t.Errorf("expected 'abcde...', got '%s'", got)
	}
}
