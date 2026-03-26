package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordRequest(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	tracker.RecordRequest(RequestRecord{
		Model:        "gpt-4o",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		DurationMs:   500,
		Backend:      "api_key",
	})

	records := tracker.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Model != "gpt-4o" {
		t.Errorf("expected model=gpt-4o, got %s", r.Model)
	}
	if r.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", r.InputTokens)
	}
	if r.OutputTokens != 50 {
		t.Errorf("expected output_tokens=50, got %d", r.OutputTokens)
	}
	if r.TotalTokens != 150 {
		t.Errorf("expected total_tokens=150, got %d", r.TotalTokens)
	}
	if r.Timestamp == "" {
		t.Error("expected auto-filled timestamp")
	}
}

func TestRecordRequestPreservesTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	ts := "2025-01-15T10:30:00Z"
	tracker.RecordRequest(RequestRecord{
		Timestamp:   ts,
		Model:       "gpt-4o",
		InputTokens: 10,
	})

	records := tracker.Records("")
	if records[0].Timestamp != ts {
		t.Errorf("expected timestamp=%s, got %s", ts, records[0].Timestamp)
	}
}

func TestMultipleRecords(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	for i := 0; i < 5; i++ {
		tracker.RecordRequest(RequestRecord{
			Model:       "gpt-4o",
			InputTokens: i * 100,
		})
	}

	records := tracker.Records("")
	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}
}

func TestSummaryEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	summary := tracker.Summary()

	allTime, ok := summary["all_time"].(map[string]interface{})
	if !ok {
		t.Fatal("missing all_time in summary")
	}
	if allTime["requests"] != 0 {
		t.Errorf("expected 0 requests, got %v", allTime["requests"])
	}
}

func TestSummaryWithRecords(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	now := time.Now().UTC().Format(time.RFC3339)
	tracker.RecordRequest(RequestRecord{
		Timestamp:    now,
		Model:        "gpt-4o",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		DurationMs:   500,
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp:    now,
		Model:        "gpt-4o-mini",
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		DurationMs:   300,
	})

	summary := tracker.Summary()

	allTime := summary["all_time"].(map[string]interface{})
	if allTime["requests"] != 2 {
		t.Errorf("expected 2 total requests, got %v", allTime["requests"])
	}
	if allTime["input_tokens"] != 300 {
		t.Errorf("expected 300 input tokens, got %v", allTime["input_tokens"])
	}
	if allTime["output_tokens"] != 150 {
		t.Errorf("expected 150 output tokens, got %v", allTime["output_tokens"])
	}

	models := summary["models"].(map[string]int)
	if models["gpt-4o"] != 1 {
		t.Errorf("expected gpt-4o count=1, got %d", models["gpt-4o"])
	}
	if models["gpt-4o-mini"] != 1 {
		t.Errorf("expected gpt-4o-mini count=1, got %d", models["gpt-4o-mini"])
	}
}

func TestRecordsSinceFilter(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	old := "2024-01-01T00:00:00Z"
	recent := time.Now().UTC().Format(time.RFC3339)

	tracker.RecordRequest(RequestRecord{Timestamp: old, Model: "gpt-4o"})
	tracker.RecordRequest(RequestRecord{Timestamp: recent, Model: "gpt-4o-mini"})

	// Filter: only records since 2025-01-01
	filtered := tracker.Records("2025-01-01T00:00:00Z")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered record, got %d", len(filtered))
	}
	if filtered[0].Model != "gpt-4o-mini" {
		t.Errorf("expected gpt-4o-mini, got %s", filtered[0].Model)
	}
}

func TestRecordsSinceBadFormat(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	tracker.RecordRequest(RequestRecord{Model: "gpt-4o"})

	// Bad format returns all records
	records := tracker.Records("not-a-date")
	if len(records) != 1 {
		t.Fatalf("expected 1 record on bad since format, got %d", len(records))
	}
}

func TestRecordsEmptyNoSince(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	records := tracker.Records("")
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create tracker and add a record.
	t1 := NewTracker(tmpDir)
	t1.RecordRequest(RequestRecord{
		Model:        "gpt-4o",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
	})

	// Create new tracker from same dir — should load persisted data.
	t2 := NewTracker(tmpDir)
	records := t2.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 persisted record, got %d", len(records))
	}
	if records[0].Model != "gpt-4o" {
		t.Errorf("expected model=gpt-4o, got %s", records[0].Model)
	}
}

func TestCorruptUsageFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Write corrupt JSON to usage file.
	os.WriteFile(filepath.Join(tmpDir, usageFile), []byte(`{corrupt`), 0o600)

	tracker := NewTracker(tmpDir)
	records := tracker.Records("")
	if len(records) != 0 {
		t.Errorf("expected 0 records after corrupt file, got %d", len(records))
	}
}

func TestSummaryAvgDuration(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	now := time.Now().UTC().Format(time.RFC3339)
	tracker.RecordRequest(RequestRecord{Timestamp: now, Model: "gpt-4o", DurationMs: 100})
	tracker.RecordRequest(RequestRecord{Timestamp: now, Model: "gpt-4o", DurationMs: 300})

	summary := tracker.Summary()
	allTime := summary["all_time"].(map[string]interface{})
	avg := allTime["avg_duration_ms"].(int64)
	if avg != 200 {
		t.Errorf("expected avg_duration_ms=200, got %d", avg)
	}
}

func TestSafeDivide(t *testing.T) {
	if safeDivide(100, 0) != 0 {
		t.Error("expected 0 for divide by zero")
	}
	if safeDivide(100, 5) != 20 {
		t.Error("expected 20 for 100/5")
	}
}

func TestPersistenceFileFormat(t *testing.T) {
	tmpDir := t.TempDir()
	tracker := NewTracker(tmpDir)

	tracker.RecordRequest(RequestRecord{Model: "gpt-4o", InputTokens: 42})

	data, err := os.ReadFile(filepath.Join(tmpDir, usageFile))
	if err != nil {
		t.Fatalf("failed to read usage file: %v", err)
	}

	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("usage file is not valid JSON: %v", err)
	}
	if len(store.Requests) != 1 {
		t.Errorf("expected 1 request in file, got %d", len(store.Requests))
	}
}
