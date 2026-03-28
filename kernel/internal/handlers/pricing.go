package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// PricingHandler manages model price CRUD.
type PricingHandler struct {
	pluginClient *http.Client // mTLS-aware client for querying plugins
}

// NewPricingHandler creates a new PricingHandler.
func NewPricingHandler(pluginClient *http.Client) *PricingHandler {
	return &PricingHandler{pluginClient: pluginClient}
}

func (h *PricingHandler) db() *gorm.DB { return database.Get() }

// ListPrices returns all pricing entries.
// GET /api/pricing
func (h *PricingHandler) ListPrices(c *gin.Context) {
	var prices []models.ModelPrice
	if err := h.db().Order("provider, model, effective_from DESC").Find(&prices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
	}
	c.JSON(http.StatusOK, prices)
}

// ListCurrentPrices returns only the currently-effective pricing entries.
// GET /api/pricing/current
func (h *PricingHandler) ListCurrentPrices(c *gin.Context) {
	var prices []models.ModelPrice
	if err := h.db().Where("effective_to IS NULL").Order("provider, model").Find(&prices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
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

// earliestUsageTimestamp queries cost-tracking for the earliest usage record
// matching the given provider+model. Returns zero time if unavailable.
func (h *PricingHandler) earliestUsageTimestamp(provider, model string) time.Time {
	if h.pluginClient == nil {
		return time.Time{}
	}

	// Look up cost-tracking plugin address.
	var plugin models.Plugin
	if err := h.db().Where("id = ? AND status = 'running'", "infra-cost-tracking").First(&plugin).Error; err != nil {
		log.Printf("pricing: cannot find running infra-cost-tracking plugin: %v", err)
		return time.Time{}
	}

	scheme := "http"
	if plugin.HTTPPort == 0 {
		return time.Time{}
	}
	// Use HTTPS if the plugin registered with a TLS port (standard for mTLS plugins).
	if h.pluginClient.Transport != nil {
		scheme = "https"
	}

	url := fmt.Sprintf("%s://%s:%d/usage/records", scheme, plugin.Host, plugin.HTTPPort)
	resp, err := h.pluginClient.Get(url)
	if err != nil {
		log.Printf("pricing: failed to query cost-tracking: %v", err)
		return time.Time{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return time.Time{}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return time.Time{}
	}

	var result struct {
		Records []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			TS       string `json:"ts"`
		} `json:"records"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return time.Time{}
	}

	// Records come sorted ASC by timestamp, find first matching provider+model.
	for _, r := range result.Records {
		if r.Provider == provider && r.Model == model {
			if t, err := time.Parse(time.RFC3339, r.TS); err == nil {
				return t
			}
		}
	}

	return time.Time{}
}

// SavePriceRecord closes any existing price window for the given provider+model
// and creates a new one. This is the shared logic used by both the admin API
// and plugin pricing endpoints.
func SavePriceRecord(db *gorm.DB, provider, model string, inputPer1M, outputPer1M, cachedPer1M, perRequest, subscription float64, currency string, effectiveFrom time.Time) (*models.ModelPrice, error) {
	if currency == "" {
		currency = "USD"
	}

	from := effectiveFrom
	if from.IsZero() {
		from = time.Now().UTC()
	}

	// Close existing window.
	db.Model(&models.ModelPrice{}).
		Where("provider = ? AND model = ? AND effective_to IS NULL", provider, model).
		Update("effective_to", from)

	// Open new window.
	price := models.ModelPrice{
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
	if err := db.Create(&price).Error; err != nil {
		return nil, err
	}
	return &price, nil
}

// SavePrice creates a new pricing window. If no previous price exists for that
// provider+model, effective_from is set to the earliest usage record timestamp
// so the price covers all historical data. Otherwise effective_from is now.
// POST /api/pricing
func (h *PricingHandler) SavePrice(c *gin.Context) {
	var req savePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Check if a prior price window exists for this provider+model.
	var count int64
	h.db().Model(&models.ModelPrice{}).
		Where("provider = ? AND model = ?", req.Provider, req.Model).
		Count(&count)

	var effectiveFrom time.Time
	if count == 0 {
		// No previous price — backfill to earliest usage record.
		effectiveFrom = h.earliestUsageTimestamp(req.Provider, req.Model)
	}
	// effectiveFrom is zero when there are existing prices or no usage records found.
	// SavePriceRecord defaults zero to time.Now().

	price, err := SavePriceRecord(h.db(), req.Provider, req.Model, req.InputPer1M, req.OutputPer1M, req.CachedPer1M, req.PerRequest, req.Subscription, req.Currency, effectiveFrom)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save price: %v", err)})
		return
	}

	c.JSON(http.StatusOK, price)
}

// DeletePrice removes a pricing entry by ID.
// DELETE /api/pricing/:id
func (h *PricingHandler) DeletePrice(c *gin.Context) {
	id := c.Param("id")

	result := h.db().Delete(&models.ModelPrice{}, id)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete price"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "price not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
