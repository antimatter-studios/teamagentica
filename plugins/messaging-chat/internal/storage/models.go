package storage

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// ThreadState holds extensible per-conversation state (persisted as JSON).
type ThreadState struct {
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
}

// Scan implements sql.Scanner for GORM JSON column.
func (s *ThreadState) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		if str, ok2 := value.(string); ok2 {
			b = []byte(str)
		} else {
			return nil
		}
	}
	return json.Unmarshal(b, s)
}

// Value implements driver.Valuer for GORM JSON column.
func (s ThreadState) Value() (driver.Value, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

type Conversation struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	UserID    uint           `json:"user_id" gorm:"index;not null"`
	Title     string         `json:"title" gorm:"not null;default:'New Chat'"`
	State     ThreadState    `json:"state" gorm:"type:text"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

type Message struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	ConversationID uint           `json:"conversation_id" gorm:"index;not null"`
	Role           string    `json:"role" gorm:"not null"`
	Content        string    `json:"content" gorm:"type:text"`
	AgentAlias     string    `json:"agent_alias,omitempty"`
	AgentPlugin    string    `json:"agent_plugin,omitempty"`
	Model          string    `json:"model,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	InputTokens    int       `json:"input_tokens,omitempty"`
	OutputTokens   int       `json:"output_tokens,omitempty"`
	CachedTokens   int       `json:"cached_tokens,omitempty"`
	CostUSD        float64   `json:"cost_usd,omitempty"`
	DurationMs     int64     `json:"duration_ms,omitempty"`
	Attachments    string         `json:"attachments,omitempty" gorm:"type:text"`
	CreatedAt      time.Time      `json:"created_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

type Attachment struct {
	Type       string `json:"type"`                         // "image", "video", "url"
	Filename   string `json:"filename"`
	FileID     string `json:"file_id,omitempty"`            // legacy local file ref
	StorageKey string `json:"storage_key,omitempty"`        // sss3 storage key
	MimeType   string `json:"mime_type"`
	URL        string `json:"url,omitempty"`                // for external URLs (videos)
}
