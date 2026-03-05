package usage

import (
	"testing"
	"time"
)

func TestRecordRequestStores(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	tracker.RecordRequest(RequestRecord{
		Model:        "kimi-k2-turbo-preview",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		DurationMs:   200,
	})

	records := tracker.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Model != "kimi-k2-turbo-preview" {
		t.Errorf("expected model kimi-k2-turbo-preview, got %s", records[0].Model)
	}
	if records[0].InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", records[0].InputTokens)
	}
	if records[0].OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", records[0].OutputTokens)
	}
	if records[0].Timestamp == "" {
		t.Error("expected timestamp to be auto-set")
	}
}

func TestRecordRequestPreservesTimestamp(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	ts := "2025-01-15T10:00:00Z"
	tracker.RecordRequest(RequestRecord{
		Timestamp:   ts,
		Model:       "moonshot-v1-8k",
		InputTokens: 10,
	})

	records := tracker.Records("")
	if records[0].Timestamp != ts {
		t.Errorf("expected timestamp %s, got %s", ts, records[0].Timestamp)
	}
}

func TestRecordsAll(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	for i := 0; i < 3; i++ {
		tracker.RecordRequest(RequestRecord{
			Model:       "kimi-k2-turbo-preview",
			InputTokens: 10 * (i + 1),
		})
	}

	records := tracker.Records("")
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
}

func TestRecordsSinceFilter(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	tracker.RecordRequest(RequestRecord{
		Timestamp:   "2025-01-01T00:00:00Z",
		Model:       "kimi-k2-turbo-preview",
		InputTokens: 10,
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp:   "2025-06-01T00:00:00Z",
		Model:       "kimi-k2-turbo-preview",
		InputTokens: 20,
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp:   "2025-12-01T00:00:00Z",
		Model:       "kimi-k2-turbo-preview",
		InputTokens: 30,
	})

	records := tracker.Records("2025-06-01T00:00:00Z")
	if len(records) != 2 {
		t.Fatalf("expected 2 records since June, got %d", len(records))
	}
	if records[0].InputTokens != 20 {
		t.Errorf("expected first filtered record to have 20 input tokens, got %d", records[0].InputTokens)
	}
}

func TestRecordsSinceInvalidFallback(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	tracker.RecordRequest(RequestRecord{
		Model:       "kimi-k2-turbo-preview",
		InputTokens: 10,
	})

	records := tracker.Records("not-a-valid-time")
	if len(records) != 1 {
		t.Fatalf("expected 1 record on invalid since, got %d", len(records))
	}
}

func TestSummaryAggregation(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	now := time.Now().UTC()
	todayTS := now.Format(time.RFC3339)

	tracker.RecordRequest(RequestRecord{
		Timestamp:    todayTS,
		Model:        "kimi-k2-turbo-preview",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		DurationMs:   200,
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp:    todayTS,
		Model:        "moonshot-v1-8k",
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		DurationMs:   400,
	})

	summary := tracker.Summary()

	allTime, ok := summary["all_time"].(map[string]interface{})
	if !ok {
		t.Fatal("all_time not a map")
	}
	if allTime["requests"] != 2 {
		t.Errorf("expected 2 total requests, got %v", allTime["requests"])
	}
	if allTime["input_tokens"] != 300 {
		t.Errorf("expected 300 total input tokens, got %v", allTime["input_tokens"])
	}
	if allTime["output_tokens"] != 150 {
		t.Errorf("expected 150 total output tokens, got %v", allTime["output_tokens"])
	}
	if allTime["total_tokens"] != 450 {
		t.Errorf("expected 450 total tokens, got %v", allTime["total_tokens"])
	}
	if allTime["avg_duration_ms"] != int64(300) {
		t.Errorf("expected avg_duration_ms=300, got %v", allTime["avg_duration_ms"])
	}

	models, ok := summary["models"].(map[string]int)
	if !ok {
		t.Fatal("models not a map[string]int")
	}
	if models["kimi-k2-turbo-preview"] != 1 {
		t.Errorf("expected 1 kimi-k2-turbo-preview, got %d", models["kimi-k2-turbo-preview"])
	}
	if models["moonshot-v1-8k"] != 1 {
		t.Errorf("expected 1 moonshot-v1-8k, got %d", models["moonshot-v1-8k"])
	}
}

func TestSummaryEmptyTracker(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	summary := tracker.Summary()

	allTime, ok := summary["all_time"].(map[string]interface{})
	if !ok {
		t.Fatal("all_time not a map")
	}
	if allTime["requests"] != 0 {
		t.Errorf("expected 0 requests, got %v", allTime["requests"])
	}
	if allTime["avg_duration_ms"] != int64(0) {
		t.Errorf("expected avg_duration_ms=0, got %v", allTime["avg_duration_ms"])
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	tracker1 := NewTracker(dir)
	tracker1.RecordRequest(RequestRecord{
		Model:       "kimi-k2-turbo-preview",
		InputTokens: 42,
	})

	tracker2 := NewTracker(dir)
	records := tracker2.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 record after reload, got %d", len(records))
	}
	if records[0].InputTokens != 42 {
		t.Errorf("expected 42 input tokens, got %d", records[0].InputTokens)
	}
}

func TestUpdateRateLimit(t *testing.T) {
	tracker := NewTracker(t.TempDir())

	headers := map[string][]string{
		"X-Ratelimit-Limit":     {"100"},
		"X-Ratelimit-Remaining": {"95"},
		"X-Ratelimit-Reset":     {"2025-06-01T00:00:00Z"},
	}
	tracker.UpdateRateLimit(headers)

	summary := tracker.Summary()
	rl, ok := summary["rate_limit"].(RateLimit)
	if !ok {
		t.Fatal("rate_limit not RateLimit type")
	}
	if rl.LimitRequests != 100 {
		t.Errorf("expected limit 100, got %d", rl.LimitRequests)
	}
	if rl.RemainingRequests != 95 {
		t.Errorf("expected remaining 95, got %d", rl.RemainingRequests)
	}
}
