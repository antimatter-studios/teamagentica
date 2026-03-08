package storage

import "time"

type Conversation struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	UserID       uint      `json:"user_id" gorm:"index;not null"`
	Title        string    `json:"title" gorm:"not null;default:'New Chat'"`
	DefaultAgent string    `json:"default_agent"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Message struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	ConversationID uint      `json:"conversation_id" gorm:"index;not null"`
	Role           string    `json:"role" gorm:"not null"`
	Content        string    `json:"content" gorm:"type:text"`
	AgentAlias     string    `json:"agent_alias,omitempty"`
	AgentPlugin    string    `json:"agent_plugin,omitempty"`
	Model          string    `json:"model,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	InputTokens    int       `json:"input_tokens,omitempty"`
	OutputTokens   int       `json:"output_tokens,omitempty"`
	DurationMs     int64     `json:"duration_ms,omitempty"`
	Attachments    string    `json:"attachments,omitempty" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at"`
}

type Attachment struct {
	Type       string `json:"type"`                         // "image", "video", "url"
	Filename   string `json:"filename"`
	FileID     string `json:"file_id,omitempty"`            // legacy local file ref
	StorageKey string `json:"storage_key,omitempty"`        // sss3 storage key
	MimeType   string `json:"mime_type"`
	URL        string `json:"url,omitempty"`                // for external URLs (videos)
}
