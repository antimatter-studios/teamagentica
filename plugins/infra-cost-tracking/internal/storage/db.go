package storage

import (
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// UsageRecord is a single API call's usage data reported by any plugin.
type UsageRecord struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	PluginID        string    `json:"plugin_id" gorm:"index;not null"`
	Provider        string    `json:"provider" gorm:"index;not null"`
	Model           string    `json:"model" gorm:"index;not null"`
	RecordType      string    `json:"record_type" gorm:"default:'token'"` // "token" or "request"
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	CachedTokens    int       `json:"cached_tokens"`
	ReasoningTokens int       `json:"reasoning_tokens"`
	DurationMs      int64     `json:"duration_ms"`
	UserID          string    `json:"user_id" gorm:"index"`
	Backend         string    `json:"backend"`
	Status          string    `json:"status"`  // for request-type: "submitted", "completed", "failed"
	Prompt          string    `json:"prompt"`  // for video tools
	TaskID          string    `json:"task_id"` // for video tools
	Timestamp       time.Time `json:"ts" gorm:"index;not null"`
	CreatedAt       time.Time `json:"created_at"`
}

// DB wraps the GORM connection for usage storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the SQLite database at dataPath/costs.db.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "costs.db", &UsageRecord{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

// Insert stores a usage record.
func (d *DB) Insert(rec *UsageRecord) error {
	return d.db.Create(rec).Error
}

// Records returns all usage records, optionally filtered by start time and user ID.
func (d *DB) Records(since, userID string) ([]UsageRecord, error) {
	query := d.db.Order("timestamp ASC")
	if since != "" {
		if sinceTime, err := time.Parse(time.RFC3339, since); err == nil {
			query = query.Where("timestamp >= ?", sinceTime)
		}
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	var records []UsageRecord
	err := query.Find(&records).Error
	return records, err
}

// Summary returns aggregate stats, optionally filtered by user ID.
func (d *DB) Summary(userID string) (map[string]interface{}, error) {
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekStart := todayStart.AddDate(0, 0, -7)

	base := d.db.Model(&UsageRecord{})
	if userID != "" {
		base = base.Where("user_id = ?", userID)
	}

	var totalCount, todayCount, weekCount int64
	base.Count(&totalCount)
	base.Where("timestamp >= ?", todayStart).Count(&todayCount)
	base.Where("timestamp >= ?", weekStart).Count(&weekCount)

	// Model breakdown.
	type modelCount struct {
		Provider string
		Model    string
		Count    int64
	}
	var breakdown []modelCount
	q := d.db.Model(&UsageRecord{})
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	q.Select("provider, model, count(*) as count").
		Group("provider, model").
		Order("count DESC").
		Scan(&breakdown)

	models := make([]map[string]interface{}, len(breakdown))
	for i, m := range breakdown {
		models[i] = map[string]interface{}{
			"provider": m.Provider,
			"model":    m.Model,
			"count":    m.Count,
		}
	}

	return map[string]interface{}{
		"total_records": totalCount,
		"today_records": todayCount,
		"week_records":  weekCount,
		"models":        models,
	}, nil
}

// DistinctUsers returns all distinct user_id values with their record counts.
func (d *DB) DistinctUsers() ([]map[string]interface{}, error) {
	type userCount struct {
		UserID string
		Count  int64
	}
	var results []userCount
	err := d.db.Model(&UsageRecord{}).
		Select("user_id, count(*) as count").
		Where("user_id != ''").
		Group("user_id").
		Order("count DESC").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, len(results))
	for i, r := range results {
		out[i] = map[string]interface{}{
			"user_id": r.UserID,
			"count":   r.Count,
		}
	}
	return out, nil
}
