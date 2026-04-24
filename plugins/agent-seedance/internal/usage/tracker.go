package usage

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const usageFile = "usage.json"

// RequestRecord is one logged video generation request.
type RequestRecord struct {
	Timestamp  string `json:"ts"`
	Model      string `json:"model"`
	Prompt     string `json:"prompt"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
}

// Store is the on-disk usage data.
type Store struct {
	Requests []RequestRecord `json:"requests"`
}

// Tracker accumulates usage data and persists it to disk.
type Tracker struct {
	mu       sync.Mutex
	dataPath string
	store    Store
}

func NewTracker(dataPath string) *Tracker {
	t := &Tracker{dataPath: dataPath}
	t.load()
	return t
}

func (t *Tracker) RecordRequest(rec RequestRecord) {
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	t.mu.Lock()
	t.store.Requests = append(t.store.Requests, rec)
	t.mu.Unlock()
	t.save()
}

func (t *Tracker) Summary() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var totalReqs, todayReqs, completedReqs, failedReqs int
	modelCounts := map[string]int{}

	for _, r := range t.store.Requests {
		ts, _ := time.Parse(time.RFC3339, r.Timestamp)
		totalReqs++
		modelCounts[r.Model]++

		if r.Status == "completed" {
			completedReqs++
		} else if r.Status == "failed" {
			failedReqs++
		}

		if !ts.Before(todayStart) {
			todayReqs++
		}
	}

	return map[string]interface{}{
		"today": map[string]interface{}{
			"requests": todayReqs,
		},
		"all_time": map[string]interface{}{
			"requests":  totalReqs,
			"completed": completedReqs,
			"failed":    failedReqs,
		},
		"models": modelCounts,
	}
}

// Records returns raw request records, optionally filtered by a start time.
func (t *Tracker) Records(since string) []RequestRecord {
	t.mu.Lock()
	defer t.mu.Unlock()

	if since == "" {
		out := make([]RequestRecord, len(t.store.Requests))
		copy(out, t.store.Requests)
		return out
	}

	sinceTime, err := time.Parse(time.RFC3339, since)
	if err != nil {
		out := make([]RequestRecord, len(t.store.Requests))
		copy(out, t.store.Requests)
		return out
	}

	var out []RequestRecord
	for _, r := range t.store.Requests {
		ts, _ := time.Parse(time.RFC3339, r.Timestamp)
		if !ts.Before(sinceTime) {
			out = append(out, r)
		}
	}
	return out
}

func (t *Tracker) filePath() string {
	return filepath.Join(t.dataPath, usageFile)
}

func (t *Tracker) load() {
	data, err := os.ReadFile(t.filePath())
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &t.store); err != nil {
		log.Printf("[usage] corrupt usage file, starting fresh: %v", err)
		t.store = Store{}
	}
}

func (t *Tracker) save() {
	t.mu.Lock()
	data, err := json.Marshal(t.store)
	t.mu.Unlock()
	if err != nil {
		log.Printf("[usage] marshal error: %v", err)
		return
	}
	if err := os.MkdirAll(t.dataPath, 0o700); err != nil {
		log.Printf("[usage] mkdir error: %v", err)
		return
	}
	if err := os.WriteFile(t.filePath(), data, 0o600); err != nil {
		log.Printf("[usage] write error: %v", err)
	}
}
