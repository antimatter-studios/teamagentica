package storage

import (
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func TestOpen(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestInsertAndRecords(t *testing.T) {
	db := openTestDB(t)

	rec := &UsageRecord{
		PluginID:     "agent-openai",
		Provider:     "openai",
		Model:        "gpt-4o",
		RecordType:   "token",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		Timestamp:    time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC),
	}
	if err := db.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if rec.ID == 0 {
		t.Fatal("expected auto-generated ID > 0")
	}

	records, err := db.Records("", "")
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Provider != "openai" {
		t.Errorf("expected provider openai, got %s", records[0].Provider)
	}
	if records[0].InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", records[0].InputTokens)
	}
}

func TestRecordsSinceFilter(t *testing.T) {
	db := openTestDB(t)

	old := &UsageRecord{
		PluginID:  "p1",
		Provider:  "openai",
		Model:     "gpt-4o",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	recent := &UsageRecord{
		PluginID:  "p1",
		Provider:  "anthropic",
		Model:     "claude-3",
		Timestamp: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	for _, r := range []*UsageRecord{old, recent} {
		if err := db.Insert(r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// No filter: both records.
	all, err := db.Records("", "")
	if err != nil {
		t.Fatalf("Records (no filter): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 records, got %d", len(all))
	}

	// Filter since 2026-02-01: only the recent one.
	filtered, err := db.Records("2026-02-01T00:00:00Z", "")
	if err != nil {
		t.Fatalf("Records (since): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 record after filter, got %d", len(filtered))
	}
	if filtered[0].Provider != "anthropic" {
		t.Errorf("expected anthropic, got %s", filtered[0].Provider)
	}
}

func TestRecordsSinceInvalidIgnored(t *testing.T) {
	db := openTestDB(t)

	rec := &UsageRecord{
		PluginID:  "p1",
		Provider:  "openai",
		Model:     "gpt-4o",
		Timestamp: time.Now().UTC(),
	}
	if err := db.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Invalid since string should be ignored and return all records.
	records, err := db.Records("not-a-date", "")
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record (invalid since ignored), got %d", len(records))
	}
}

func TestSummary(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC()
	records := []*UsageRecord{
		{PluginID: "p1", Provider: "openai", Model: "gpt-4o", Timestamp: now},
		{PluginID: "p1", Provider: "openai", Model: "gpt-4o", Timestamp: now},
		{PluginID: "p2", Provider: "anthropic", Model: "claude-3", Timestamp: now},
		// An old record outside today but within the week.
		{PluginID: "p1", Provider: "openai", Model: "gpt-4o", Timestamp: now.AddDate(0, 0, -2)},
	}
	for _, r := range records {
		if err := db.Insert(r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	summary, err := db.Summary("")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	total, ok := summary["total_records"].(int64)
	if !ok {
		t.Fatalf("total_records not int64: %T", summary["total_records"])
	}
	if total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}

	todayCount, _ := summary["today_records"].(int64)
	if todayCount != 3 {
		t.Errorf("expected today 3, got %d", todayCount)
	}

	weekCount, _ := summary["week_records"].(int64)
	if weekCount != 4 {
		t.Errorf("expected week 4, got %d", weekCount)
	}

	models, ok := summary["models"].([]map[string]interface{})
	if !ok {
		t.Fatalf("models wrong type: %T", summary["models"])
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 model groups, got %d", len(models))
	}

	// First group should be openai/gpt-4o (count 3, highest).
	if models[0]["provider"] != "openai" || models[0]["model"] != "gpt-4o" {
		t.Errorf("unexpected first model group: %v", models[0])
	}
	if models[0]["count"] != int64(3) {
		t.Errorf("expected openai count 3, got %v", models[0]["count"])
	}
}

func TestSummaryEmpty(t *testing.T) {
	db := openTestDB(t)

	summary, err := db.Summary("")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	total, _ := summary["total_records"].(int64)
	if total != 0 {
		t.Errorf("expected 0 total, got %d", total)
	}
}
