package memory

import "context"

// Message is a single conversation turn sent to the memory provider.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Memory is a stored fact or extracted piece of knowledge.
type Memory struct {
	ID         string         `json:"id"`
	Text       string         `json:"memory"`
	UserID     string         `json:"user_id,omitempty"`
	AgentID    string         `json:"agent_id,omitempty"`
	AppID      string         `json:"app_id,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Categories []string       `json:"categories,omitempty"`
	Immutable  bool           `json:"immutable,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
	Score      float64        `json:"score,omitempty"`
}

// Entity represents a user, agent, app, or run known to the memory system.
type Entity struct {
	Type string `json:"type"` // "user", "agent", "app", "run"
	ID   string `json:"id"`
}

// AddOpts configures how memories are added.
type AddOpts struct {
	UserID             string         `json:"user_id,omitempty"`
	AgentID            string         `json:"agent_id,omitempty"`
	AppID              string         `json:"app_id,omitempty"`
	RunID              string         `json:"run_id,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	Infer              *bool          `json:"infer,omitempty"`               // true = extract facts from messages
	Immutable          *bool          `json:"immutable,omitempty"`           // true = memory cannot be updated
	EnableGraph        *bool          `json:"enable_graph,omitempty"`        // enable graph memory
	ExpirationDate     string         `json:"expiration_date,omitempty"`     // ISO 8601
	CustomCategories   []string       `json:"custom_categories,omitempty"`   // constrain to these categories
	CustomInstructions string         `json:"custom_instructions,omitempty"` // extra extraction guidance
}

// SearchOpts configures memory search.
type SearchOpts struct {
	Filters       map[string]any `json:"filters"`
	TopK          int            `json:"top_k,omitempty"`
	Threshold     float64        `json:"threshold,omitempty"`
	Rerank        *bool          `json:"rerank,omitempty"`
	KeywordSearch *bool          `json:"keyword_search,omitempty"`
}

// ListOpts configures memory listing.
type ListOpts struct {
	Filters  map[string]any `json:"filters"`
	Page     int            `json:"page,omitempty"`
	PageSize int            `json:"page_size,omitempty"`
	Fields   []string       `json:"fields,omitempty"`
}

// Provider is the abstraction layer over any memory backend.
// Swap the implementation to change from Mem0 to another system.
type Provider interface {
	// Add sends messages to the memory system for fact extraction and storage.
	Add(ctx context.Context, messages []Message, opts AddOpts) ([]Memory, error)

	// Search performs semantic search across stored memories.
	Search(ctx context.Context, query string, opts SearchOpts) ([]Memory, error)

	// List returns memories matching the given filters with pagination.
	List(ctx context.Context, opts ListOpts) ([]Memory, error)

	// Get retrieves a single memory by ID.
	Get(ctx context.Context, memoryID string) (*Memory, error)

	// Update modifies a memory's text and/or metadata.
	Update(ctx context.Context, memoryID string, text string, metadata map[string]any) error

	// Delete removes a single memory by ID.
	Delete(ctx context.Context, memoryID string) error

	// DeleteAll removes all memories matching the given scope filters.
	DeleteAll(ctx context.Context, filters map[string]any) error

	// DeleteEntities hard-deletes an entity and all its associated memories.
	DeleteEntities(ctx context.Context, entityType, entityID string) error

	// ListEntities enumerates known users, agents, apps, and runs.
	ListEntities(ctx context.Context) ([]Entity, error)

	// Healthy returns nil if the backend is reachable.
	Healthy(ctx context.Context) error
}
