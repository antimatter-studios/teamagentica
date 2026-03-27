package debugtrace

import (
	"encoding/json"
	"log"
	"path/filepath"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Recorder writes structured trace rows to SQLite when debug mode is on.
// All methods are safe to call when the recorder is nil (no-op).
type Recorder struct {
	db *gorm.DB
}

// Row types written to the traces table.
const (
	TypeRequest       = "request"
	TypeTaskCall      = "task_call"
	TypeTaskResponse  = "task_response"
	TypeFinalResponse = "final_response"
)

// Attachment holds mime type and base64 data or URL for storage in traces.
type Attachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data,omitempty"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// Trace is the GORM model for the traces table.
type Trace struct {
	ID              string         `gorm:"primaryKey" json:"id"`
	RequestID       string         `gorm:"index;not null" json:"request_id"`
	ParentID        string         `json:"parent_id,omitempty"`
	Type            string         `gorm:"not null" json:"type"`
	Alias           string         `json:"alias,omitempty"`
	PluginID        string         `json:"plugin_id,omitempty"`
	TaskID          string         `json:"task_id,omitempty"`
	Message         string         `json:"message,omitempty"`
	Attachments     int            `json:"attachments"`
	AttachmentsData string         `json:"attachments_data,omitempty"`
	Model           string         `json:"model,omitempty"`
	DurationMS      int64          `json:"duration_ms,omitempty"`
	Error           string         `json:"error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	DeletedAt       gorm.DeletedAt `json:"-" gorm:"index"`
}

// Open creates or opens the trace database at the given path.
func Open(dbPath string) (*Recorder, error) {
	dir := filepath.Dir(dbPath)
	name := filepath.Base(dbPath)
	db, err := pluginsdk.OpenDatabase(dir, name, &Trace{})
	if err != nil {
		return nil, err
	}

	// Prune traces older than 24h on startup.
	db.Where("created_at < ?", time.Now().Add(-24*time.Hour)).Delete(&Trace{})

	log.Printf("Debug trace database opened: %s", dbPath)
	return &Recorder{db: db}, nil
}

// Close closes the database.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	sqlDB, err := r.db.DB()
	if err == nil {
		sqlDB.Close()
	}
}

// NewRequestID generates a new request trace ID.
func NewRequestID() string {
	return uuid.New().String()
}

// Record inserts a trace row. Safe to call on nil receiver (no-op).
func (r *Recorder) Record(requestID, parentID, traceType, alias, pluginID, taskID, message string, attachments []Attachment, model string, durationMS int64, traceErr string) string {
	if r == nil {
		return ""
	}

	id := uuid.New().String()

	var attachData string
	attachCount := len(attachments)
	if attachCount > 0 {
		b, _ := json.Marshal(attachments)
		attachData = string(b)
	}

	trace := Trace{
		ID:              id,
		RequestID:       requestID,
		ParentID:        parentID,
		Type:            traceType,
		Alias:           alias,
		PluginID:        pluginID,
		TaskID:          taskID,
		Message:         message,
		Attachments:     attachCount,
		AttachmentsData: attachData,
		Model:           model,
		DurationMS:      durationMS,
		Error:           traceErr,
		CreatedAt:       time.Now().UTC(),
	}

	if err := r.db.Create(&trace).Error; err != nil {
		log.Printf("trace record error: %v", err)
	}

	return id
}

// ListRequests returns the most recent top-level request traces.
func (r *Recorder) ListRequests(limit int) ([]Trace, error) {
	if r == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	var traces []Trace
	err := r.db.Where("type = ?", TypeRequest).Order("created_at DESC").Limit(limit).Find(&traces).Error
	return traces, err
}

// GetTrace returns all rows for a given request ID, ordered chronologically.
func (r *Recorder) GetTrace(requestID string) ([]Trace, error) {
	if r == nil {
		return nil, nil
	}

	var traces []Trace
	err := r.db.Where("request_id = ?", requestID).Order("created_at ASC").Find(&traces).Error
	return traces, err
}
