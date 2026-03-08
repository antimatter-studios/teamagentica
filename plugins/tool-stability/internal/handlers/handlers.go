package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-stability/internal/stability"
	"github.com/antimatter-studios/teamagentica/plugins/tool-stability/internal/usage"
)

type Handler struct {
	apiKey string
	model  string
	debug  bool
	sdk    *pluginsdk.Client
	client *stability.Client
	usage  *usage.Tracker
}

func NewHandler(apiKey, model, dataPath string, debug bool) *Handler {
	return &Handler{
		apiKey: apiKey,
		model:  model,
		debug:  debug,
		client: stability.NewClient(apiKey, model, debug),
		usage:  usage.NewTracker(dataPath),
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"plugin":     "tool-stability",
		"version":    "1.0.0",
		"configured": h.apiKey != "",
		"model":      h.model,
	})
}

type generateRequest struct {
	Prompt         string `json:"prompt" binding:"required"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
}

// Generate creates an image from a text prompt via Stability AI.
func (h *Handler) Generate(c *gin.Context) {
	if h.apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No API key configured. Set STABILITY_API_KEY."})
		return
	}

	var req generateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: prompt is required"})
		return
	}

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	h.emitEvent("generate_request", fmt.Sprintf("model=%s prompt=%s", h.model, truncateStr(req.Prompt, 100)))

	start := time.Now()

	result, err := h.client.Generate(stability.GenerateRequest{
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		AspectRatio:    req.AspectRatio,
		OutputFormat:   req.OutputFormat,
	})
	if err != nil {
		log.Printf("Stability generate error: %v", err)
		h.emitEvent("error", fmt.Sprintf("generate: %v", err))

		h.usage.RecordRequest(usage.RequestRecord{
			Model:      h.model,
			Prompt:     truncateStr(req.Prompt, 200),
			Status:     "failed",
			DurationMs: time.Since(start).Milliseconds(),
		})

		if h.sdk != nil {
			h.sdk.ReportUsage(pluginsdk.UsageReport{
				UserID:     userID,
				Provider:   "stability",
				Model:      h.model,
				RecordType: "request",
				Status:     "failed",
				Prompt:     truncateStr(req.Prompt, 200),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}

		c.JSON(http.StatusBadGateway, gin.H{"error": "Image generation failed: " + err.Error()})
		return
	}

	elapsed := time.Since(start)

	h.usage.RecordRequest(usage.RequestRecord{
		Model:      h.model,
		Prompt:     truncateStr(req.Prompt, 200),
		Status:     "completed",
		DurationMs: elapsed.Milliseconds(),
	})

	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:     userID,
			Provider:   "stability",
			Model:      h.model,
			RecordType: "request",
			Status:     "completed",
			Prompt:     truncateStr(req.Prompt, 200),
			DurationMs: elapsed.Milliseconds(),
		})
	}

	h.emitEvent("generate_complete", fmt.Sprintf("model=%s duration=%dms seed=%s", h.model, elapsed.Milliseconds(), result.Seed))

	c.JSON(http.StatusOK, gin.H{
		"status":     "completed",
		"image_data": result.ImageData,
		"mime_type":  result.MimeType,
		"seed":       result.Seed,
		"model":      h.model,
		"prompt":     req.Prompt,
	})
}

// ConfigOptions returns dynamic select options for a config field.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")

	switch field {
	case "STABILITY_MODEL":
		if h.apiKey == "" {
			c.JSON(http.StatusOK, gin.H{"options": stability.DefaultModels(), "fallback": true})
			return
		}
		models, fallback, err := h.client.ListModels()
		if err != nil {
			log.Printf("ListModels error: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"options": models, "fallback": fallback})
	default:
		c.JSON(http.StatusOK, gin.H{"options": []string{}, "error": "Unknown field"})
	}
}

// Usage returns accumulated usage stats.
func (h *Handler) Usage(c *gin.Context) {
	c.JSON(http.StatusOK, h.usage.Summary())
}

// UsageRecords returns raw request records, optionally filtered by ?since=RFC3339.
func (h *Handler) UsageRecords(c *gin.Context) {
	since := c.Query("since")
	records := h.usage.Records(since)
	if records == nil {
		records = []usage.RequestRecord{}
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
