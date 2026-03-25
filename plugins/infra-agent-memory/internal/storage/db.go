package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// Message is a single turn in a conversation session.
type Message struct {
	ID        uint           `json:"id" gorm:"primaryKey;autoIncrement"`
	SessionID string         `json:"session_id" gorm:"not null;index:idx_session_created"`
	Role      string         `json:"role" gorm:"not null"` // "user" | "assistant"
	Content   string         `json:"content" gorm:"not null"`
	Responder string         `json:"responder" gorm:"default:''"`
	CreatedAt time.Time      `json:"created_at" gorm:"index:idx_session_created"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// Memory is a structured fact extracted from conversations or saved by agents.
type Memory struct {
	ID            uint           `json:"id" gorm:"primaryKey;autoIncrement"`
	Category      string         `json:"category" gorm:"not null;index:idx_mem_category"` // user_fact, decision, project, reference, general
	Content       string         `json:"content" gorm:"not null"`
	Tags          string         `json:"tags" gorm:"default:''"` // comma-separated
	SourceAgent   string         `json:"source_agent" gorm:"default:'';index:idx_mem_agent"`
	SourceSession string         `json:"source_session" gorm:"default:''"`
	Importance    int            `json:"importance" gorm:"default:5"` // 1-10, higher = more important
	AccessCount   int            `json:"access_count" gorm:"default:0"`
	LastAccessed  *time.Time     `json:"last_accessed"`
	CreatedAt     time.Time      `json:"created_at" gorm:"index"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

// MemorySummary is a lightweight view for listing.
type MemorySummary struct {
	ID         uint      `json:"id"`
	Category   string    `json:"category"`
	Content    string    `json:"content"`
	Tags       string    `json:"tags"`
	Importance int       `json:"importance"`
	CreatedAt  time.Time `json:"created_at"`
}

// SessionSummary is a lightweight view of a session for listing.
type SessionSummary struct {
	SessionID    string    `json:"session_id"`
	MessageCount int       `json:"message_count"`
	LastActivity time.Time `json:"last_activity"`
}

// DB wraps the GORM connection for memory storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the SQLite database at dataPath/memory.db.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "memory.db", &Message{}, &Memory{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

// AddMessage appends a message to the given session.
func (d *DB) AddMessage(sessionID, role, content, responder string) (*Message, error) {
	m := &Message{
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Responder: responder,
		CreatedAt: time.Now(),
	}
	if err := d.db.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

// GetHistory returns up to limit messages for a session, ordered oldest-first.
// If limit <= 0, returns all messages.
func (d *DB) GetHistory(sessionID string, limit int) ([]Message, error) {
	var msgs []Message
	q := d.db.Where("session_id = ?", sessionID).Order("created_at ASC")
	if limit > 0 {
		// Fetch the most recent `limit` messages but return them oldest-first.
		var count int64
		d.db.Model(&Message{}).Where("session_id = ?", sessionID).Count(&count)
		if int(count) > limit {
			q = q.Offset(int(count) - limit)
		}
	}
	err := q.Find(&msgs).Error
	return msgs, err
}

// ClearSession deletes all messages for a session.
func (d *DB) ClearSession(sessionID string) error {
	return d.db.Where("session_id = ?", sessionID).Delete(&Message{}).Error
}

// ListSessions returns a summary of all sessions ordered by most recent activity.
func (d *DB) ListSessions() ([]SessionSummary, error) {
	var rows []struct {
		SessionID    string
		MessageCount int
		LastActivity time.Time
	}
	err := d.db.Model(&Message{}).
		Select("session_id, COUNT(*) as message_count, MAX(created_at) as last_activity").
		Group("session_id").
		Order("last_activity DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	summaries := make([]SessionSummary, len(rows))
	for i, r := range rows {
		summaries[i] = SessionSummary{
			SessionID:    r.SessionID,
			MessageCount: r.MessageCount,
			LastActivity: r.LastActivity,
		}
	}
	return summaries, nil
}

// PruneSession removes oldest messages from a session, keeping only maxMessages.
func (d *DB) PruneSession(sessionID string, maxMessages int) error {
	if maxMessages <= 0 {
		return nil
	}
	var count int64
	d.db.Model(&Message{}).Where("session_id = ?", sessionID).Count(&count)
	if int(count) <= maxMessages {
		return nil
	}
	// Find the ID of the message at position (count - maxMessages) from the oldest.
	var cutoff Message
	err := d.db.Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Offset(int(count) - maxMessages).
		First(&cutoff).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.db.Where("session_id = ? AND id < ?", sessionID, cutoff.ID).Delete(&Message{}).Error
}

// PruneExpiredSessions deletes all sessions with no activity in ttlHours.
func (d *DB) PruneExpiredSessions(ttlHours int) (int64, error) {
	if ttlHours <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-time.Duration(ttlHours) * time.Hour)
	// Find session IDs where max(created_at) < cutoff.
	var expired []string
	err := d.db.Model(&Message{}).
		Select("session_id").
		Group("session_id").
		Having("MAX(created_at) < ?", cutoff).
		Pluck("session_id", &expired).Error
	if err != nil {
		return 0, err
	}
	if len(expired) == 0 {
		return 0, nil
	}
	result := d.db.Where("session_id IN ?", expired).Delete(&Message{})
	return result.RowsAffected, result.Error
}

// ── Memory (facts) operations ────────────────────────────────────────────────

// SaveMemory stores a new fact.
func (d *DB) SaveMemory(category, content, tags, sourceAgent, sourceSession string, importance int) (*Memory, error) {
	if importance < 1 || importance > 10 {
		importance = 5
	}
	now := time.Now()
	m := &Memory{
		Category:      category,
		Content:       content,
		Tags:          tags,
		SourceAgent:   sourceAgent,
		SourceSession: sourceSession,
		Importance:    importance,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := d.db.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

// UpdateMemory modifies an existing memory's content, tags, category, or importance.
func (d *DB) UpdateMemory(id uint, updates map[string]interface{}) (*Memory, error) {
	var m Memory
	if err := d.db.First(&m, id).Error; err != nil {
		return nil, err
	}
	updates["updated_at"] = time.Now()
	if err := d.db.Model(&m).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteMemory soft-deletes a memory by ID.
func (d *DB) DeleteMemory(id uint) error {
	return d.db.Delete(&Memory{}, id).Error
}

// GetMemory returns a single memory by ID.
func (d *DB) GetMemory(id uint) (*Memory, error) {
	var m Memory
	if err := d.db.First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// RecallMemory searches memories using keyword matching across content and tags.
// Results are ranked by: keyword relevance, importance, recency.
func (d *DB) RecallMemory(query string, category string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}

	// Split query into keywords for matching.
	keywords := strings.Fields(strings.ToLower(query))
	if len(keywords) == 0 {
		return d.ListMemories(category, limit)
	}

	// Build LIKE conditions for each keyword across content and tags.
	tx := d.db.Model(&Memory{})
	if category != "" {
		tx = tx.Where("category = ?", category)
	}

	// Each keyword must appear in either content or tags.
	for _, kw := range keywords {
		pattern := fmt.Sprintf("%%%s%%", kw)
		tx = tx.Where("(LOWER(content) LIKE ? OR LOWER(tags) LIKE ?)", pattern, pattern)
	}

	var results []Memory
	err := tx.Order("importance DESC, updated_at DESC").
		Limit(limit).
		Find(&results).Error
	if err != nil {
		return nil, err
	}

	// Bump access counts for returned memories.
	if len(results) > 0 {
		ids := make([]uint, len(results))
		for i, r := range results {
			ids[i] = r.ID
		}
		now := time.Now()
		d.db.Model(&Memory{}).Where("id IN ?", ids).
			Updates(map[string]interface{}{"access_count": gorm.Expr("access_count + 1"), "last_accessed": now})
	}

	return results, nil
}

// ListMemories returns memories optionally filtered by category, ordered by importance then recency.
func (d *DB) ListMemories(category string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	tx := d.db.Model(&Memory{})
	if category != "" {
		tx = tx.Where("category = ?", category)
	}
	var results []Memory
	err := tx.Order("importance DESC, updated_at DESC").Limit(limit).Find(&results).Error
	return results, err
}
