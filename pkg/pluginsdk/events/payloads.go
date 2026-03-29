package events

// AliasRegistryUpdatePayload is emitted when the alias registry changes.
type AliasRegistryUpdatePayload struct {
	Aliases []AliasEntry `json:"aliases,omitempty"`
}

// AliasEntry represents a single alias in an update event.
type AliasEntry struct {
	Name     string `json:"name"`
	PluginID string `json:"plugin_id"`
	Type     string `json:"type,omitempty"`
	Model    string `json:"model,omitempty"`
}

// PersonaUpdatePayload is emitted when a persona changes.
type PersonaUpdatePayload struct {
	PersonaID string `json:"persona_id,omitempty"`
	Alias     string `json:"alias,omitempty"`
}

// ConfigUpdatePayload is emitted when plugin config changes.
type ConfigUpdatePayload struct {
	PluginID string            `json:"plugin_id"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// MCPServerPayload is emitted when the MCP server starts or stops.
type MCPServerPayload struct {
	Endpoint string `json:"endpoint,omitempty"`
}

// RelayProgressPayload is emitted during message processing.
type RelayProgressPayload struct {
	TaskGroupID  string `json:"task_group_id,omitempty"`
	Status       string `json:"status,omitempty"` // thinking, running, completed, failed
	AgentAlias   string `json:"agent_alias,omitempty"`
	Content      string `json:"content,omitempty"`
	Model        string `json:"model,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	CachedTokens int    `json:"cached_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
}

// WebhookReadyPayload is emitted when a webhook ingress is ready.
type WebhookReadyPayload struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// WebhookRoutePayload is emitted when a webhook route is registered.
type WebhookRoutePayload struct {
	PluginID   string `json:"plugin_id"`
	Prefix     string `json:"prefix"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

// WebhookPluginURLPayload is emitted with the public URL for a plugin's webhook.
type WebhookPluginURLPayload struct {
	PluginID string `json:"plugin_id"`
	URL      string `json:"url"`
}

// IngressReadyPayload is emitted when an ingress tunnel is ready.
type IngressReadyPayload struct {
	URL string `json:"url"`
}

// UserPayload is emitted for user lifecycle events.
type UserPayload struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email,omitempty"`
}

// SchedulerFiredPayload is emitted when a scheduled job executes.
type SchedulerFiredPayload struct {
	JobName string `json:"job_name"`
	Text    string `json:"text,omitempty"`
}

// TaskAssignPayload is emitted when a task is assigned.
type TaskAssignPayload struct {
	CardID   string `json:"card_id"`
	CardTitle string `json:"card_title"`
	Agent    string `json:"agent,omitempty"`
}

// TaskCommentPayload is emitted when a task receives a comment.
type TaskCommentPayload struct {
	CardID  string `json:"card_id"`
	Comment string `json:"comment"`
}

// StatusPayload is a simple status message for events that carry just a string.
type StatusPayload struct {
	Message string `json:"message,omitempty"`
}
