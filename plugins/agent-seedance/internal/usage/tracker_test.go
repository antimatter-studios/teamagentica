package usage

import (
	"testing"
	"time"
)

func TestRecordAndRetrieve(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	tracker.RecordRequest(RequestRecord{
		Model:  "seedance-2.0",
		Prompt: "ocean waves",
		TaskID: "seed-1",
		Status: "submitted",
	})

	records := tracker.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Model != "seedance-2.0" {
		t.Errorf("expected model seedance-2.0, got %s", records[0].Model)
	}
	if records[0].TaskID != "seed-1" {
		t.Errorf("expected task_id seed-1, got %s", records[0].TaskID)
	}
	if records[0].Timestamp == "" {
		t.Error("expected auto-populated timestamp")
	}
}

func TestRecordPreservesTimestamp(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	ts := "2025-01-15T10:00:00Z"
	tracker.RecordRequest(RequestRecord{
		Timestamp: ts,
		Model:     "seedance-2.0",
		Prompt:    "test",
		Status:    "completed",
	})

	records := tracker.Records("")
	if records[0].Timestamp != ts {
		t.Errorf("expected timestamp %s, got %s", ts, records[0].Timestamp)
	}
}

func TestRecordsSinceFilter(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	tracker.RecordRequest(RequestRecord{
		Timestamp: "2025-01-01T00:00:00Z",
		Model:     "seedance-2.0",
		Prompt:    "old",
		Status:    "completed",
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp: "2025-06-01T00:00:00Z",
		Model:     "seedance-2.0",
		Prompt:    "new",
		Status:    "completed",
	})

	records := tracker.Records("2025-03-01T00:00:00Z")
	if len(records) != 1 {
		t.Fatalf("expected 1 record after filter, got %d", len(records))
	}
	if records[0].Prompt != "new" {
		t.Errorf("expected prompt 'new', got '%s'", records[0].Prompt)
	}
}

func TestRecordsSinceInvalidFallback(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	tracker.RecordRequest(RequestRecord{
		Model:  "seedance-2.0",
		Prompt: "test",
		Status: "submitted",
	})

	records := tracker.Records("not-a-date")
	if len(records) != 1 {
		t.Errorf("expected all records on invalid since, got %d", len(records))
	}
}

func TestSummary(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	now := time.Now().UTC().Format(time.RFC3339)

	tracker.RecordRequest(RequestRecord{
		Timestamp: now,
		Model:     "seedance-2.0",
		Prompt:    "test1",
		Status:    "completed",
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp: now,
		Model:     "seedance-1.0-pro",
		Prompt:    "test2",
		Status:    "failed",
	})
	tracker.RecordRequest(RequestRecord{
		Timestamp: "2024-01-01T00:00:00Z",
		Model:     "seedance-2.0",
		Prompt:    "old",
		Status:    "completed",
	})

	summary := tracker.Summary()

	allTime, ok := summary["all_time"].(map[string]interface{})
	if !ok {
		t.Fatal("missing all_time in summary")
	}
	if allTime["requests"] != 3 {
		t.Errorf("expected 3 total requests, got %v", allTime["requests"])
	}
	if allTime["completed"] != 2 {
		t.Errorf("expected 2 completed, got %v", allTime["completed"])
	}
	if allTime["failed"] != 1 {
		t.Errorf("expected 1 failed, got %v", allTime["failed"])
	}

	today, ok := summary["today"].(map[string]interface{})
	if !ok {
		t.Fatal("missing today in summary")
	}
	if today["requests"] != 2 {
		t.Errorf("expected 2 today requests, got %v", today["requests"])
	}

	models, ok := summary["models"].(map[string]int)
	if !ok {
		t.Fatal("missing models in summary")
	}
	if models["seedance-2.0"] != 2 {
		t.Errorf("expected seedance-2.0 count 2, got %d", models["seedance-2.0"])
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	tracker1 := NewTracker(dir)
	tracker1.RecordRequest(RequestRecord{
		Model:  "seedance-2.0",
		Prompt: "persist test",
		Status: "submitted",
	})

	tracker2 := NewTracker(dir)
	records := tracker2.Records("")
	if len(records) != 1 {
		t.Fatalf("expected 1 persisted record, got %d", len(records))
	}
	if records[0].Prompt != "persist test" {
		t.Errorf("expected prompt 'persist test', got '%s'", records[0].Prompt)
	}
}

func TestEmptyRecords(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	records := tracker.Records("")
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}
