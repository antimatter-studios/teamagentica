package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/cost-explorer/internal/storage"
)

// Handler holds plugin dependencies.
type Handler struct {
	db  *storage.DB
	sdk *pluginsdk.Client
}

// NewHandler creates a new Handler.
func NewHandler(db *storage.DB) *Handler {
	return &Handler{db: db}
}

// SetSDK attaches the plugin SDK client.
func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

// Health returns a simple health check.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"plugin":  "cost-explorer",
		"version": "1.0.0",
	})
}

// reportRequest is the body for POST /usage.
type reportRequest struct {
	PluginID        string `json:"plugin_id" binding:"required"`
	Provider        string `json:"provider" binding:"required"`
	Model           string `json:"model" binding:"required"`
	RecordType      string `json:"record_type"`
	UserID          string `json:"user_id"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	TotalTokens     int    `json:"total_tokens"`
	CachedTokens    int    `json:"cached_tokens"`
	ReasoningTokens int    `json:"reasoning_tokens"`
	DurationMs      int64  `json:"duration_ms"`
	Backend         string `json:"backend"`
	Status          string `json:"status"`
	Prompt          string `json:"prompt"`
	TaskID          string `json:"task_id"`
	Timestamp       string `json:"ts"`
}

// ReportUsage receives and stores a usage record from any plugin.
// POST /usage
func (h *Handler) ReportUsage(c *gin.Context) {
	var req reportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ts := time.Now().UTC()
	if req.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, req.Timestamp); err == nil {
			ts = parsed
		}
	}

	recordType := req.RecordType
	if recordType == "" {
		recordType = "token"
	}

	rec := &storage.UsageRecord{
		PluginID:        req.PluginID,
		Provider:        req.Provider,
		Model:           req.Model,
		RecordType:      recordType,
		UserID:          req.UserID,
		InputTokens:     req.InputTokens,
		OutputTokens:    req.OutputTokens,
		TotalTokens:     req.TotalTokens,
		CachedTokens:    req.CachedTokens,
		ReasoningTokens: req.ReasoningTokens,
		DurationMs:      req.DurationMs,
		Backend:         req.Backend,
		Status:          req.Status,
		Prompt:          req.Prompt,
		TaskID:          req.TaskID,
		Timestamp:       ts,
	}

	if err := h.db.Insert(rec); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store record"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": rec.ID})
}

// ListRecords returns all usage records, optionally filtered by ?since=RFC3339 and ?user_id=.
// GET /usage/records
func (h *Handler) ListRecords(c *gin.Context) {
	since := c.Query("since")
	userID := c.Query("user_id")
	records, err := h.db.Records(since, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query records"})
		return
	}
	if records == nil {
		records = []storage.UsageRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}

// Summary returns aggregate stats, optionally filtered by ?user_id=.
// GET /usage
func (h *Handler) Summary(c *gin.Context) {
	userID := c.Query("user_id")
	summary, err := h.db.Summary(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build summary"})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// HandleUsageEvent receives a usage:report event from the kernel's addressed delivery system.
// POST /events/usage
// The kernel sends: {event_type, plugin_id, detail, timestamp}
// Returning 200 tells the kernel to remove this event from the pending queue.
func (h *Handler) HandleUsageEvent(c *gin.Context) {
	var envelope struct {
		EventType string `json:"event_type"`
		PluginID  string `json:"plugin_id"`
		Detail    string `json:"detail"`
		Timestamp string `json:"timestamp"`
	}
	if err := c.ShouldBindJSON(&envelope); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event envelope"})
		return
	}

	// Parse the usage report from the detail string.
	var report pluginsdk.UsageReport
	if err := json.Unmarshal([]byte(envelope.Detail), &report); err != nil {
		log.Printf("cost-explorer: failed to parse usage detail from %s: %v", envelope.PluginID, err)
		// Return 200 anyway to prevent infinite retry of malformed events.
		c.JSON(http.StatusOK, gin.H{"message": "skipped (parse error)"})
		return
	}

	ts := time.Now().UTC()
	if envelope.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, envelope.Timestamp); err == nil {
			ts = parsed
		}
	}

	recordType := report.RecordType
	if recordType == "" {
		recordType = "token"
	}

	rec := &storage.UsageRecord{
		PluginID:     envelope.PluginID,
		Provider:     report.Provider,
		Model:        report.Model,
		RecordType:   recordType,
		UserID:       report.UserID,
		InputTokens:  report.InputTokens,
		OutputTokens: report.OutputTokens,
		TotalTokens:  report.TotalTokens,
		CachedTokens: report.CachedTokens,
		DurationMs:   report.DurationMs,
		Status:       report.Status,
		Prompt:       report.Prompt,
		TaskID:       report.TaskID,
		Timestamp:    ts,
	}

	if err := h.db.Insert(rec); err != nil {
		log.Printf("cost-explorer: failed to store usage from %s: %v", envelope.PluginID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage failed"})
		return
	}

	log.Printf("cost-explorer: stored usage from %s provider=%s model=%s", envelope.PluginID, report.Provider, report.Model)
	c.JSON(http.StatusOK, gin.H{"message": "stored", "id": rec.ID})
}

// UsageUsers returns distinct user IDs with record counts.
// GET /usage/users
func (h *Handler) UsageUsers(c *gin.Context) {
	users, err := h.db.DistinctUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query users"})
		return
	}
	if users == nil {
		users = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}
