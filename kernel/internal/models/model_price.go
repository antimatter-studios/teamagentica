package models

import "time"

// ModelPrice stores per-model pricing with effective date windows.
// When a price is updated, the old row gets EffectiveTo set and a new row is created,
// preserving historical pricing for accurate cost calculation over time.
type ModelPrice struct {
	ID            uint       `json:"id" gorm:"primaryKey"`
	Provider      string     `json:"provider" gorm:"index"`       // "openai", "gemini", "veo", "seedance"
	Model         string     `json:"model" gorm:"index"`          // "gpt-4o", "gemini-2.5-flash", etc.
	InputPer1M    float64    `json:"input_per_1m"`                // $ per 1M input tokens
	OutputPer1M   float64    `json:"output_per_1m"`               // $ per 1M output tokens
	CachedPer1M   float64    `json:"cached_per_1m"`               // $ per 1M cached tokens
	PerRequest    float64    `json:"per_request"`                  // $ per request (for video tools)
	Subscription  float64    `json:"subscription"`                 // $ flat fee per month (overrides token-based pricing)
	Currency      string     `json:"currency" gorm:"default:'USD'"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to"`                 // nil = current price
	CreatedAt     time.Time  `json:"created_at"`
}
