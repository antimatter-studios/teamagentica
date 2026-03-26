package usage

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const usageFile = "usage.json"

// RateLimit holds the last-seen rate limit headers from the API.
type RateLimit struct {
	LimitRequests     int    `json:"limit_requests"`
	LimitTokens       int    `json:"limit_tokens"`
	RemainingRequests int    `json:"remaining_requests"`
	RemainingTokens   int    `json:"remaining_tokens"`
	ResetRequests     string `json:"reset_requests"`
	ResetTokens       string `json:"reset_tokens"`
	UpdatedAt         string `json:"updated_at"`
}

// RequestRecord is one logged API call.
type RequestRecord struct {
	Timestamp       string `json:"ts"`
	Model           string `json:"model"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	TotalTokens     int    `json:"total_tokens"`
	CachedTokens    int    `json:"cached_tokens"`
	ReasoningTokens int    `json:"reasoning_tokens"`
	DurationMs      int64  `json:"duration_ms"`
	Backend         string `json:"backend"` // "codex" or "openai"
}

// Store is the on-disk usage data.
type Store struct {
	RateLimit RateLimit       `json:"rate_limit"`
	Requests  []RequestRecord `json:"requests"`
}

// Tracker accumulates usage data and persists it to disk.
type Tracker struct {
	mu       sync.Mutex
	dataPath string
	store    Store
}

// NewTracker loads existing usage data from disk.
func NewTracker(dataPath string) *Tracker {
	t := &Tracker{dataPath: dataPath}
	t.load()
	return t
}

// RecordRequest logs a completed API call.
func (t *Tracker) RecordRequest(rec RequestRecord) {
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	t.mu.Lock()
	t.store.Requests = append(t.store.Requests, rec)
	t.mu.Unlock()
	t.save()
}

// UpdateRateLimit captures rate limit headers from an HTTP response.
func (t *Tracker) UpdateRateLimit(resp *http.Response) {
	rl := RateLimit{
		LimitRequests:     headerInt(resp, "X-Ratelimit-Limit-Requests"),
		LimitTokens:       headerInt(resp, "X-Ratelimit-Limit-Tokens"),
		RemainingRequests: headerInt(resp, "X-Ratelimit-Remaining-Requests"),
		RemainingTokens:   headerInt(resp, "X-Ratelimit-Remaining-Tokens"),
		ResetRequests:     resp.Header.Get("X-Ratelimit-Reset-Requests"),
		ResetTokens:       resp.Header.Get("X-Ratelimit-Reset-Tokens"),
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	// Only update if we actually got header data.
	if rl.LimitRequests > 0 || rl.LimitTokens > 0 ||
		rl.RemainingRequests > 0 || rl.RemainingTokens > 0 {
		t.mu.Lock()
		t.store.RateLimit = rl
		t.mu.Unlock()
		t.save()
	}
}

// Summary returns aggregate stats for display.
func (t *Tracker) Summary() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekStart := todayStart.AddDate(0, 0, -int(now.Weekday()))

	var (
		totalReqs, todayReqs, weekReqs             int
		totalIn, totalOut, totalAll                 int
		todayIn, todayOut, todayAll                 int
		weekIn, weekOut, weekAll                    int
		totalCached, todayCached, weekCached        int
		totalReasoning, todayReasoning, weekReasoning int
		totalDuration, todayDuration, weekDuration  int64
	)

	modelCounts := map[string]int{}

	for _, r := range t.store.Requests {
		ts, _ := time.Parse(time.RFC3339, r.Timestamp)

		totalReqs++
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalAll += r.TotalTokens
		totalCached += r.CachedTokens
		totalReasoning += r.ReasoningTokens
		totalDuration += r.DurationMs
		modelCounts[r.Model]++

		if !ts.Before(todayStart) {
			todayReqs++
			todayIn += r.InputTokens
			todayOut += r.OutputTokens
			todayAll += r.TotalTokens
			todayCached += r.CachedTokens
			todayReasoning += r.ReasoningTokens
			todayDuration += r.DurationMs
		}
		if !ts.Before(weekStart) {
			weekReqs++
			weekIn += r.InputTokens
			weekOut += r.OutputTokens
			weekAll += r.TotalTokens
			weekCached += r.CachedTokens
			weekReasoning += r.ReasoningTokens
			weekDuration += r.DurationMs
		}
	}

	avgDuration := int64(0)
	if totalReqs > 0 {
		avgDuration = totalDuration / int64(totalReqs)
	}

	return map[string]interface{}{
		"rate_limit": t.store.RateLimit,
		"today": map[string]interface{}{
			"requests":         todayReqs,
			"input_tokens":     todayIn,
			"output_tokens":    todayOut,
			"total_tokens":     todayAll,
			"cached_tokens":    todayCached,
			"reasoning_tokens": todayReasoning,
			"avg_duration_ms":  safeDivide(todayDuration, int64(todayReqs)),
		},
		"week": map[string]interface{}{
			"requests":         weekReqs,
			"input_tokens":     weekIn,
			"output_tokens":    weekOut,
			"total_tokens":     weekAll,
			"cached_tokens":    weekCached,
			"reasoning_tokens": weekReasoning,
			"avg_duration_ms":  safeDivide(weekDuration, int64(weekReqs)),
		},
		"all_time": map[string]interface{}{
			"requests":         totalReqs,
			"input_tokens":     totalIn,
			"output_tokens":    totalOut,
			"total_tokens":     totalAll,
			"cached_tokens":    totalCached,
			"reasoning_tokens": totalReasoning,
			"avg_duration_ms":  avgDuration,
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
		// Bad format — return all.
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

// --- persistence ---

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

// --- helpers ---

func headerInt(resp *http.Response, key string) int {
	v := resp.Header.Get(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

func safeDivide(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	return a / b
}
