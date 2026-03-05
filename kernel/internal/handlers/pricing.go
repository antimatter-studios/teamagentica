package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// PricingHandler manages model price CRUD.
type PricingHandler struct {
	db *gorm.DB
}

// NewPricingHandler creates a new PricingHandler.
func NewPricingHandler(db *gorm.DB) *PricingHandler {
	return &PricingHandler{db: db}
}

// ListPrices returns all pricing entries.
// GET /api/pricing
func (h *PricingHandler) ListPrices(c *gin.Context) {
	var prices []models.ModelPrice
	if err := h.db.Order("provider, model, effective_from DESC").Find(&prices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
	}
	c.JSON(http.StatusOK, prices)
}

// ListCurrentPrices returns only the currently-effective pricing entries.
// GET /api/pricing/current
func (h *PricingHandler) ListCurrentPrices(c *gin.Context) {
	var prices []models.ModelPrice
	if err := h.db.Where("effective_to IS NULL").Order("provider, model").Find(&prices).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query prices"})
		return
	}
	c.JSON(http.StatusOK, prices)
}

// savePriceRequest is the body for creating/updating a price.
type savePriceRequest struct {
	Provider    string  `json:"provider" binding:"required"`
	Model       string  `json:"model" binding:"required"`
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
	CachedPer1M float64 `json:"cached_per_1m"`
	PerRequest  float64 `json:"per_request"`
	Currency    string  `json:"currency"`
}

// SavePriceRecord closes any existing price window for the given provider+model
// and creates a new one. This is the shared logic used by both the admin API
// and plugin pricing endpoints.
func SavePriceRecord(db *gorm.DB, provider, model string, inputPer1M, outputPer1M, cachedPer1M, perRequest float64, currency string) (*models.ModelPrice, error) {
	now := time.Now().UTC()
	if currency == "" {
		currency = "USD"
	}

	// Close existing window.
	db.Model(&models.ModelPrice{}).
		Where("provider = ? AND model = ? AND effective_to IS NULL", provider, model).
		Update("effective_to", now)

	// Open new window.
	price := models.ModelPrice{
		Provider:      provider,
		Model:         model,
		InputPer1M:    inputPer1M,
		OutputPer1M:   outputPer1M,
		CachedPer1M:   cachedPer1M,
		PerRequest:    perRequest,
		Currency:      currency,
		EffectiveFrom: now,
	}
	if err := db.Create(&price).Error; err != nil {
		return nil, err
	}
	return &price, nil
}

// SavePrice creates a new pricing window. If a current price exists for that
// provider+model, it closes the old window (sets effective_to = now) and opens
// a new one. This preserves historical pricing for accurate cost calculation.
// POST /api/pricing
func (h *PricingHandler) SavePrice(c *gin.Context) {
	var req savePriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	price, err := SavePriceRecord(h.db, req.Provider, req.Model, req.InputPer1M, req.OutputPer1M, req.CachedPer1M, req.PerRequest, req.Currency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save price"})
		return
	}

	c.JSON(http.StatusOK, price)
}

// DeletePrice removes a pricing entry by ID.
// DELETE /api/pricing/:id
func (h *PricingHandler) DeletePrice(c *gin.Context) {
	id := c.Param("id")

	result := h.db.Delete(&models.ModelPrice{}, id)
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
