package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-cost-tracking/internal/storage"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	return NewHandler(db)
}

func TestHealth(t *testing.T) {
	h := setupHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
	if body["plugin"] != "infra-cost-tracking" {
		t.Errorf("expected plugin infra-cost-tracking, got %v", body["plugin"])
	}
}

func TestReportUsage(t *testing.T) {
	h := setupHandler(t)

	payload := map[string]interface{}{
		"plugin_id":    "agent-openai",
		"provider":     "openai",
		"model":        "gpt-4o",
		"input_tokens": 100,
		"output_tokens": 50,
		"total_tokens": 150,
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ReportUsage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["id"] == nil || resp["id"].(float64) == 0 {
		t.Errorf("expected non-zero id, got %v", resp["id"])
	}
}

func TestReportUsageBadBody(t *testing.T) {
	h := setupHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ReportUsage(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListRecords(t *testing.T) {
	h := setupHandler(t)

	// Insert a record via ReportUsage first.
	payload := map[string]interface{}{
		"plugin_id": "p1",
		"provider":  "openai",
		"model":     "gpt-4o",
	}
	body, _ := json.Marshal(payload)
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodPost, "/usage", bytes.NewReader(body))
	c1.Request.Header.Set("Content-Type", "application/json")
	h.ReportUsage(c1)
	if w1.Code != http.StatusOK {
		t.Fatalf("setup insert failed: %d", w1.Code)
	}

	// Now list.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/records", nil)

	h.ListRecords(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Records []storage.UsageRecord `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(resp.Records))
	}
	if resp.Records[0].Provider != "openai" {
		t.Errorf("expected openai, got %s", resp.Records[0].Provider)
	}
}

func TestListRecordsEmpty(t *testing.T) {
	h := setupHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/records", nil)

	h.ListRecords(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Records []storage.UsageRecord `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(resp.Records))
	}
}

func TestSummaryHandler(t *testing.T) {
	h := setupHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage", nil)

	h.Summary(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total_records"] == nil {
		t.Error("expected total_records in summary")
	}
}

func TestHandleUsageEvent(t *testing.T) {
	h := setupHandler(t)

	detail := `{"provider":"openai","model":"gpt-4o","input_tokens":100,"output_tokens":50}`
	envelope := map[string]string{
		"event_type": "usage:report",
		"plugin_id":  "agent-openai",
		"detail":     detail,
		"timestamp":  "2026-03-03T12:00:00Z",
	}
	body, _ := json.Marshal(envelope)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/events/usage", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.HandleUsageEvent(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["message"] != "stored" {
		t.Errorf("expected message 'stored', got %v", resp["message"])
	}
	if resp["id"] == nil || resp["id"].(float64) == 0 {
		t.Errorf("expected non-zero id, got %v", resp["id"])
	}
}

func TestHandleUsageEventMalformedDetail(t *testing.T) {
	h := setupHandler(t)

	envelope := map[string]string{
		"event_type": "usage:report",
		"plugin_id":  "agent-openai",
		"detail":     "not-valid-json{{{",
		"timestamp":  "2026-03-03T12:00:00Z",
	}
	body, _ := json.Marshal(envelope)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/events/usage", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.HandleUsageEvent(c)

	// Should still return 200 to prevent infinite retry.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for malformed detail, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg, _ := resp["message"].(string)
	if msg != "skipped (parse error)" {
		t.Errorf("expected 'skipped (parse error)', got %q", msg)
	}
}

func TestHandleUsageEventBadEnvelope(t *testing.T) {
	h := setupHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/events/usage", bytes.NewReader([]byte("not json")))
	c.Request.Header.Set("Content-Type", "application/json")

	h.HandleUsageEvent(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad envelope, got %d", w.Code)
	}
}
