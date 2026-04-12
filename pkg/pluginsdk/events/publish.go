package events

import (
	"encoding/json"
	"log"
)

// Publisher is the interface required to emit events. Satisfied by pluginsdk.Client.
type Publisher interface {
	PublishEvent(eventType, detail string)
	PublishEventTo(eventType, detail, destination string)
}

// marshal is a helper that serializes a payload and logs on failure.
func marshal(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return "{}"
	}
	return string(data)
}

// --- Alias & Registry ---

// PublishAliasRegistryReady signals that the alias registry has initialized.
func PublishAliasRegistryReady(p Publisher) {
	p.PublishEvent(AliasRegistryReady, "")
}

// PublishAliasRegistryUpdate signals that the alias/agent list has changed.
func PublishAliasRegistryUpdate(p Publisher, aliases []AliasEntry) {
	p.PublishEvent(AliasRegistryUpdate, marshal(AliasRegistryUpdatePayload{Aliases: aliases}))
}

// PublishAliasUpdate signals an individual alias change.
func PublishAliasUpdate(p Publisher, name, pluginID, aliasType string) {
	p.PublishEvent(AliasUpdate, marshal(AliasEntry{Name: name, PluginID: pluginID, Type: aliasType}))
}

// --- Persona ---

// PublishAgentUpdate signals an agent has been modified.
func PublishAgentUpdate(p Publisher, agentID, alias string) {
	p.PublishEvent(AgentUpdate, marshal(AgentUpdatePayload{AgentID: agentID, Alias: alias}))
}

// PublishAgentReady signals that the agent registry has initialized.
func PublishAgentReady(p Publisher) {
	p.PublishEvent(AgentReady, "")
}

// --- MCP Server ---

// PublishMCPEnabled signals that the MCP server endpoint is available.
func PublishMCPEnabled(p Publisher, endpoint string) {
	p.PublishEvent(MCPServerEnabled, marshal(MCPServerPayload{Endpoint: endpoint}))
}

// PublishMCPDisabled signals that the MCP server endpoint has shut down.
func PublishMCPDisabled(p Publisher) {
	p.PublishEvent(MCPServerDisabled, "{}")
}

// PublishMCPToolsChanged signals that the MCP tool list has been updated.
func PublishMCPToolsChanged(p Publisher) {
	p.PublishEvent(MCPToolsChanged, "{}")
}

// --- Relay ---

// PublishRelayReady signals that the relay is accepting chat requests.
func PublishRelayReady(p Publisher) {
	p.PublishEvent(RelayReady, marshal(StatusPayload{Message: "accepting chat requests"}))
}

// PublishRelayProgress sends a progress update to the originating messaging plugin.
func PublishRelayProgress(p Publisher, sourcePlugin string, progress RelayProgressPayload) {
	p.PublishEventTo(RelayProgress, marshal(progress), sourcePlugin)
}

// PublishRelayCoordinator sends a coordinator event to the relay.
func PublishRelayCoordinator(p Publisher, detail string) {
	p.PublishEventTo(RelayCoordinator, detail, "infra-agent-relay")
}

// --- Ingress & Webhooks ---

// PublishIngressReady signals that an ingress tunnel is active with the given URL.
func PublishIngressReady(p Publisher, url string) {
	p.PublishEvent(IngressReady, marshal(IngressReadyPayload{URL: url}))
}

// PublishWebhookReady signals that the webhook ingress is listening.
func PublishWebhookReady(p Publisher, host string, port int) {
	p.PublishEvent(WebhookReady, marshal(WebhookReadyPayload{Host: host, Port: port}))
}

// PublishWebhookRoute signals a webhook route was registered.
func PublishWebhookRoute(p Publisher, pluginID, prefix, targetHost string, targetPort int) {
	p.PublishEvent(WebhookRoute, marshal(WebhookRoutePayload{
		PluginID: pluginID, Prefix: prefix, TargetHost: targetHost, TargetPort: targetPort,
	}))
}

// PublishWebhookRegister emits a debug event for route registration.
func PublishWebhookRegister(p Publisher, pluginID, prefix, targetHost string, targetPort int) {
	p.PublishEvent(WebhookRegister, marshal(WebhookRoutePayload{
		PluginID: pluginID, Prefix: prefix, TargetHost: targetHost, TargetPort: targetPort,
	}))
}

// PublishWebhookUnregister emits a debug event for route removal.
func PublishWebhookUnregister(p Publisher, pluginID string) {
	p.PublishEvent(WebhookUnregister, marshal(struct {
		PluginID string `json:"plugin_id"`
	}{PluginID: pluginID}))
}

// PublishWebhookError emits a webhook error event.
func PublishWebhookError(p Publisher, pluginID, path, errMsg string) {
	p.PublishEvent(WebhookError, marshal(struct {
		PluginID string `json:"plugin_id"`
		Path     string `json:"path"`
		Error    string `json:"error"`
	}{PluginID: pluginID, Path: path, Error: errMsg}))
}

// PublishWebhookPluginURL sends the public webhook URL to a specific plugin.
func PublishWebhookPluginURL(p Publisher, pluginID, url string) {
	p.PublishEventTo(WebhookPluginURL, marshal(WebhookPluginURLPayload{
		PluginID: pluginID, URL: url,
	}), pluginID)
}

// --- Users & Auth ---

// PublishUserRegistered signals a new user has signed up.
func PublishUserRegistered(p Publisher, userID int, email string) {
	p.PublishEvent(UserRegistered, marshal(UserPayload{UserID: userID, Email: email}))
}

// PublishUserUpdated signals a user profile has changed.
func PublishUserUpdated(p Publisher, userID int) {
	p.PublishEvent(UserUpdated, marshal(UserPayload{UserID: userID}))
}

// PublishUserDeleted signals a user has been removed.
func PublishUserDeleted(p Publisher, userID int) {
	p.PublishEvent(UserDeleted, marshal(UserPayload{UserID: userID}))
}

// --- Scheduling & Tasks ---

// PublishSchedulerFired signals a scheduled job has executed.
func PublishSchedulerFired(p Publisher, jobName, text string) {
	p.PublishEvent(SchedulerFired, marshal(SchedulerFiredPayload{JobName: jobName, Text: text}))
}

// PublishDispatchCompleted signals a task dispatch has completed.
func PublishDispatchCompleted(p Publisher, cardTitle string) {
	p.PublishEvent(DispatchCompleted, marshal(StatusPayload{Message: cardTitle}))
}

// PublishTaskAssign signals a task has been assigned.
func PublishTaskAssign(p Publisher, cardID, cardTitle, agent string) {
	p.PublishEvent(TaskTrackingAssign, marshal(TaskAssignPayload{
		CardID: cardID, CardTitle: cardTitle, Agent: agent,
	}))
}

// PublishTaskComment signals a task has received a comment.
func PublishTaskComment(p Publisher, cardID, comment string) {
	p.PublishEvent(TaskTrackingComment, marshal(TaskCommentPayload{
		CardID: cardID, Comment: comment,
	}))
}

// --- Messaging ---

// PublishPollStart signals that polling mode has started.
func PublishPollStart(p Publisher, message string) {
	p.PublishEvent(PollStart, marshal(StatusPayload{Message: message}))
}

// PublishPollStop signals that polling mode has stopped.
func PublishPollStop(p Publisher, message string) {
	p.PublishEvent(PollStop, marshal(StatusPayload{Message: message}))
}

// PublishStatus emits a generic status event with the given type and message.
func PublishStatus(p Publisher, eventType, message string) {
	p.PublishEvent(eventType, marshal(StatusPayload{Message: message}))
}
