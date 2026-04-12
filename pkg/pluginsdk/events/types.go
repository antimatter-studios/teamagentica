// Package events defines all Team Agentica platform event types, payloads,
// and factory functions. Every plugin and the kernel import this package
// for consistent event handling with no raw strings or manual serialization.
package events

// Event type constants. Use these instead of raw strings.
const (
	// Alias & Registry
	AliasRegistryReady  = "alias-registry:ready"
	AliasRegistryUpdate = "alias-registry:update"
	AliasUpdate         = "alias:update"

	// Agent Registry
	AgentUpdate = "agent:update"
	AgentReady  = "agent:ready"

	// Config
	ConfigUpdate = "config:update"

	// MCP Server
	MCPServerEnabled      = "mcp_server:enabled"
	MCPServerDisabled     = "mcp_server:disabled"
	MCPToolsChanged       = "mcp:tools_changed"

	// Relay
	RelayReady        = "relay:ready"
	RelayProgress     = "relay:progress"
	RelayTaskProgress = "relay:task:progress"
	RelayCoordinator  = "relay:coordinator"

	// Ingress & Webhooks
	WebhookReady      = "webhook:ready"
	WebhookRoute      = "webhook:route"
	WebhookRegister   = "webhook:register"
	WebhookUnregister = "webhook:unregister"
	WebhookError      = "webhook:error"
	WebhookAPIUpdate  = "webhook:api:update"
	WebhookPluginURL  = "webhook:plugin:url"
	IngressReady      = "ingress:ready"
	IngressRequest    = "ingress:request"

	// Users & Auth
	UserRegistered = "user.registered"
	UserUpdated    = "user.updated"
	UserDeleted    = "user.deleted"

	// Scheduling & Tasks
	SchedulerFired     = "scheduler:fired"
	TaskTrackingAssign = "task-tracking:assign"
	TaskTrackingComment = "task-tracking:comment"
	DispatchCompleted  = "dispatch:completed"

	// Usage & Cost
	UsageReport = "usage:report"

	// Messaging
	PollStart = "poll_start"
	PollStop  = "poll_stop"

	// Plugin lifecycle
	PluginRegistered = "plugin:registered"
)
