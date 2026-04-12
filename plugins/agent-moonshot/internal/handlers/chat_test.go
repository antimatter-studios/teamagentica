package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestHandler(apiKey, model, dataPath string) *Handler {
	return NewHandler(apiKey, model, dataPath, false, "")
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

	if resp["plugin"] != "agent-moonshot" {
		t.Errorf("expected plugin=agent-moonshot, got %v", resp["plugin"])
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

func TestChatStreamNoAPIKey(t *testing.T) {
	h := newTestHandler("", "kimi-k2-turbo-preview", t.TempDir())

	ch := h.ChatStream(context.Background(), pluginsdk.AgentChatRequest{
		Message: "hello",
	})

	var got pluginsdk.AgentStreamEvent
	for ev := range ch {
		got = ev
	}
	if got.Type != "error" {
		t.Fatalf("expected error event, got %s", got.Type)
	}
}

func TestChatStreamEmptyMessage(t *testing.T) {
	h := newTestHandler("test-key", "kimi-k2-turbo-preview", t.TempDir())

	ch := h.ChatStream(context.Background(), pluginsdk.AgentChatRequest{})

	var got pluginsdk.AgentStreamEvent
	for ev := range ch {
		got = ev
	}
	if got.Type != "error" {
		t.Fatalf("expected error event, got %s", got.Type)
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
