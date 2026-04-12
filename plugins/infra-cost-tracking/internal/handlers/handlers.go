package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-cost-tracking/internal/storage"
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
		"plugin":  "infra-cost-tracking",
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
		log.Printf("infra-cost-tracking: failed to parse usage detail from %s: %v", envelope.PluginID, err)
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
		log.Printf("infra-cost-tracking: failed to store usage from %s: %v", envelope.PluginID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage failed"})
		return
	}

	log.Printf("infra-cost-tracking: stored usage from %s provider=%s model=%s", envelope.PluginID, report.Provider, report.Model)
	c.JSON(http.StatusOK, gin.H{"message": "stored", "id": rec.ID})
}

// ProcessUsageEvent handles a usage:report event from the SDK event bus.
func (h *Handler) ProcessUsageEvent(event pluginsdk.EventCallback) {
	var report pluginsdk.UsageReport
	if err := json.Unmarshal([]byte(event.Detail), &report); err != nil {
		log.Printf("infra-cost-tracking: failed to parse usage detail from %s: %v", event.PluginID, err)
		return
	}

	ts := time.Now().UTC()
	if event.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
			ts = parsed
		}
	}

	recordType := report.RecordType
	if recordType == "" {
		recordType = "token"
	}

	rec := &storage.UsageRecord{
		PluginID:     event.PluginID,
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
		log.Printf("infra-cost-tracking: failed to store usage from %s: %v", event.PluginID, err)
		return
	}

	log.Printf("infra-cost-tracking: stored usage from %s provider=%s model=%s in=%d out=%d",
		event.PluginID, report.Provider, report.Model, report.InputTokens, report.OutputTokens)
}

// --- Pricing handlers ---

// ListPrices returns all pricing entries.
// GET /pricing
func (h *Handler) ListPrices(c *gin.Context) {
	prices, err := h.db.ListPrices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
	}
	if prices == nil {
		prices = []storage.ModelPrice{}
	}
	c.JSON(http.StatusOK, prices)
}

// ListCurrentPrices returns only the currently-effective pricing entries.
// GET /pricing/current
func (h *Handler) ListCurrentPrices(c *gin.Context) {
	prices, err := h.db.ListCurrentPrices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
	}
	if prices == nil {
		prices = []storage.ModelPrice{}
	}
	c.JSON(http.StatusOK, prices)
}

// savePriceRequest is the body for creating/updating a price.
type savePriceRequest struct {
	Provider     string  `json:"provider" binding:"required"`
	Model        string  `json:"model" binding:"required"`
	InputPer1M   float64 `json:"input_per_1m"`
	OutputPer1M  float64 `json:"output_per_1m"`
	CachedPer1M  float64 `json:"cached_per_1m"`
	PerRequest   float64 `json:"per_request"`
	Subscription float64 `json:"subscription"`
	Currency     string  `json:"currency"`
}

// savePriceRecord closes any existing price window for the given provider+model
// and creates a new one.
func (h *Handler) savePriceRecord(provider, model string, inputPer1M, outputPer1M, cachedPer1M, perRequest, subscription float64, currency string, effectiveFrom time.Time) (*storage.ModelPrice, error) {
	if currency == "" {
		currency = "USD"
	}

	from := effectiveFrom
	if from.IsZero() {
		from = time.Now().UTC()
	}

	// Close existing window.
	h.db.ClosePrice(provider, model, from)

	// Open new window.
	price := &storage.ModelPrice{
		Provider:      provider,
		Model:         model,
		InputPer1M:    inputPer1M,
		OutputPer1M:   outputPer1M,
		CachedPer1M:   cachedPer1M,
		PerRequest:    perRequest,
		Subscription:  subscription,
		Currency:      currency,
		EffectiveFrom: from,
	}
	if err := h.db.SavePrice(price); err != nil {
		return nil, err
	}
	return price, nil
}

// SavePrice creates a new pricing window. If no previous price exists for that
// provider+model, effective_from is set to the earliest usage record timestamp
// so the price covers all historical data.
// POST /pricing
func (h *Handler) SavePrice(c *gin.Context) {
	var req savePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	count, err := h.db.CountPrices(req.Provider, req.Model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check prices"})
		return
	}

	var effectiveFrom time.Time
	if count == 0 {
		// No previous price — backfill to earliest usage record (local query).
		effectiveFrom = h.db.EarliestUsageTimestamp(req.Provider, req.Model)
	}

	price, err := h.savePriceRecord(req.Provider, req.Model, req.InputPer1M, req.OutputPer1M, req.CachedPer1M, req.PerRequest, req.Subscription, req.Currency, effectiveFrom)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save price"})
		return
	}

	c.JSON(http.StatusOK, price)
}

// DeletePrice removes a pricing entry by ID.
// DELETE /pricing/:id
func (h *Handler) DeletePrice(c *gin.Context) {
	id := c.Param("id")

	var idUint uint
	if _, err := fmt.Sscanf(id, "%d", &idUint); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.db.DeletePrice(idUint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete price"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// PushPrices accepts an array of prices from plugins.
// POST /pricing/push
func (h *Handler) PushPrices(c *gin.Context) {
	var req struct {
		Prices []struct {
			Provider    string  `json:"provider" binding:"required"`
			Model       string  `json:"model" binding:"required"`
			InputPer1M  float64 `json:"input_per_1m"`
			OutputPer1M float64 `json:"output_per_1m"`
			CachedPer1M float64 `json:"cached_per_1m"`
			PerRequest  float64 `json:"per_request"`
			Currency    string  `json:"currency"`
		} `json:"prices" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var count int
	for _, p := range req.Prices {
		_, err := h.savePriceRecord(p.Provider, p.Model, p.InputPer1M, p.OutputPer1M, p.CachedPer1M, p.PerRequest, 0, p.Currency, time.Time{})
		if err != nil {
			log.Printf("pricing: failed to save %s/%s: %v", p.Provider, p.Model, err)
			continue
		}
		count++
	}

	c.JSON(http.StatusOK, gin.H{"message": "prices updated", "count": count})
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
