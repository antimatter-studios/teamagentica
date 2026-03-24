package storage

import (
	"errors"
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
	conn, err := pluginsdk.OpenDatabase(dataPath, "memory.db", &Message{})
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
