package events

import (
	"encoding/json"
	"log"
)

// Publisher is the interface required to emit events. Satisfied by pluginsdk.Client.
type Publisher interface {
	ReportEvent(eventType, detail string)
	ReportAddressedEvent(eventType, detail, destination string)
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
	p.ReportEvent(AliasRegistryReady, "")
}

// PublishAliasRegistryUpdate signals that the alias/agent list has changed.
func PublishAliasRegistryUpdate(p Publisher, aliases []AliasEntry) {
	p.ReportEvent(AliasRegistryUpdate, marshal(AliasRegistryUpdatePayload{Aliases: aliases}))
}

// PublishAliasUpdate signals an individual alias change.
func PublishAliasUpdate(p Publisher, name, pluginID, aliasType string) {
	p.ReportEvent(AliasUpdate, marshal(AliasEntry{Name: name, PluginID: pluginID, Type: aliasType}))
}

// --- Persona ---

// PublishPersonaUpdate signals a persona has been modified.
func PublishPersonaUpdate(p Publisher, personaID, alias string) {
	p.ReportEvent(PersonaUpdate, marshal(PersonaUpdatePayload{PersonaID: personaID, Alias: alias}))
}

// --- MCP Server ---

// PublishMCPEnabled signals that the MCP server endpoint is available.
func PublishMCPEnabled(p Publisher, endpoint string) {
	p.ReportEvent(MCPServerEnabled, marshal(MCPServerPayload{Endpoint: endpoint}))
}

// PublishMCPDisabled signals that the MCP server endpoint has shut down.
func PublishMCPDisabled(p Publisher) {
	p.ReportEvent(MCPServerDisabled, "{}")
}

// --- Relay ---

// PublishRelayReady signals that the relay is accepting chat requests.
func PublishRelayReady(p Publisher) {
	p.ReportEvent(RelayReady, marshal(StatusPayload{Message: "accepting chat requests"}))
}

// PublishRelayProgress sends a progress update to the originating messaging plugin.
func PublishRelayProgress(p Publisher, sourcePlugin string, progress RelayProgressPayload) {
	p.ReportAddressedEvent(RelayProgress, marshal(progress), sourcePlugin)
}

// PublishRelayCoordinator sends a coordinator event to the relay.
func PublishRelayCoordinator(p Publisher, detail string) {
	p.ReportAddressedEvent(RelayCoordinator, detail, "infra-agent-relay")
}

// --- Ingress & Webhooks ---

// PublishIngressReady signals that an ingress tunnel is active with the given URL.
func PublishIngressReady(p Publisher, url string) {
	p.ReportEvent(IngressReady, marshal(IngressReadyPayload{URL: url}))
}

// PublishWebhookReady signals that the webhook ingress is listening.
func PublishWebhookReady(p Publisher, host string, port int) {
	p.ReportEvent(WebhookReady, marshal(WebhookReadyPayload{Host: host, Port: port}))
}

// PublishWebhookRoute signals a webhook route was registered.
func PublishWebhookRoute(p Publisher, pluginID, prefix, targetHost string, targetPort int) {
	p.ReportEvent(WebhookRoute, marshal(WebhookRoutePayload{
		PluginID: pluginID, Prefix: prefix, TargetHost: targetHost, TargetPort: targetPort,
	}))
}

// PublishWebhookRegister emits a debug event for route registration.
func PublishWebhookRegister(p Publisher, pluginID, prefix, targetHost string, targetPort int) {
	p.ReportEvent(WebhookRegister, marshal(WebhookRoutePayload{
		PluginID: pluginID, Prefix: prefix, TargetHost: targetHost, TargetPort: targetPort,
	}))
}

// PublishWebhookUnregister emits a debug event for route removal.
func PublishWebhookUnregister(p Publisher, pluginID string) {
	p.ReportEvent(WebhookUnregister, marshal(struct {
		PluginID string `json:"plugin_id"`
	}{PluginID: pluginID}))
}

// PublishWebhookError emits a webhook error event.
func PublishWebhookError(p Publisher, pluginID, path, errMsg string) {
	p.ReportEvent(WebhookError, marshal(struct {
		PluginID string `json:"plugin_id"`
		Path     string `json:"path"`
		Error    string `json:"error"`
	}{PluginID: pluginID, Path: path, Error: errMsg}))
}

// PublishWebhookPluginURL sends the public webhook URL to a specific plugin.
func PublishWebhookPluginURL(p Publisher, pluginID, url string) {
	p.ReportAddressedEvent(WebhookPluginURL, marshal(WebhookPluginURLPayload{
		PluginID: pluginID, URL: url,
	}), pluginID)
}

// --- Users & Auth ---

// PublishUserRegistered signals a new user has signed up.
func PublishUserRegistered(p Publisher, userID int, email string) {
	p.ReportEvent(UserRegistered, marshal(UserPayload{UserID: userID, Email: email}))
}

// PublishUserUpdated signals a user profile has changed.
func PublishUserUpdated(p Publisher, userID int) {
	p.ReportEvent(UserUpdated, marshal(UserPayload{UserID: userID}))
}

// PublishUserDeleted signals a user has been removed.
func PublishUserDeleted(p Publisher, userID int) {
	p.ReportEvent(UserDeleted, marshal(UserPayload{UserID: userID}))
}

// --- Scheduling & Tasks ---

// PublishSchedulerFired signals a scheduled job has executed.
func PublishSchedulerFired(p Publisher, jobName, text string) {
	p.ReportEvent(SchedulerFired, marshal(SchedulerFiredPayload{JobName: jobName, Text: text}))
}

// PublishDispatchCompleted signals a task dispatch has completed.
func PublishDispatchCompleted(p Publisher, cardTitle string) {
	p.ReportEvent(DispatchCompleted, marshal(StatusPayload{Message: cardTitle}))
}

// PublishTaskAssign signals a task has been assigned.
func PublishTaskAssign(p Publisher, cardID, cardTitle, agent string) {
	p.ReportEvent(TaskTrackingAssign, marshal(TaskAssignPayload{
		CardID: cardID, CardTitle: cardTitle, Agent: agent,
	}))
}

// PublishTaskComment signals a task has received a comment.
func PublishTaskComment(p Publisher, cardID, comment string) {
	p.ReportEvent(TaskTrackingComment, marshal(TaskCommentPayload{
		CardID: cardID, Comment: comment,
	}))
}

// --- Messaging ---

// PublishPollStart signals that polling mode has started.
func PublishPollStart(p Publisher, message string) {
	p.ReportEvent(PollStart, marshal(StatusPayload{Message: message}))
}

// PublishPollStop signals that polling mode has stopped.
func PublishPollStop(p Publisher, message string) {
	p.ReportEvent(PollStop, marshal(StatusPayload{Message: message}))
}

// PublishStatus emits a generic status event with the given type and message.
func PublishStatus(p Publisher, eventType, message string) {
	p.ReportEvent(eventType, marshal(StatusPayload{Message: message}))
}
