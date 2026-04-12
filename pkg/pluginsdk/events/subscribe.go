package events

import (
	"encoding/json"
	"log"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// OnAliasRegistryUpdate subscribes to alias registry changes with a typed payload.
func OnAliasRegistryUpdate(client *pluginsdk.Client, handler func(AliasRegistryUpdatePayload)) {
	client.Events().On(AliasRegistryUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p AliasRegistryUpdatePayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", AliasRegistryUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnAliasUpdate subscribes to individual alias changes with a typed payload.
func OnAliasUpdate(client *pluginsdk.Client, handler func(AliasEntry)) {
	client.Events().On(AliasUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p AliasEntry
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", AliasUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnAgentUpdate subscribes to agent changes with a typed payload.
func OnAgentUpdate(client *pluginsdk.Client, handler func(AgentUpdatePayload)) {
	client.Events().On(AgentUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p AgentUpdatePayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", AgentUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnConfigUpdate subscribes to config changes with a typed payload.
// Registers the handler and subscribes via the EventClient.
func OnConfigUpdate(client *pluginsdk.Client, handler func(ConfigUpdatePayload)) {
	client.Events().On(ConfigUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p ConfigUpdatePayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", ConfigUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnMCPEnabled subscribes to MCP server availability with a typed payload.
func OnMCPEnabled(client *pluginsdk.Client, handler func(MCPServerPayload)) {
	client.Events().On(MCPServerEnabled, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p MCPServerPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", MCPServerEnabled, err)
			return
		}
		handler(p)
	}))
}

// OnMCPDisabled subscribes to MCP server shutdown.
func OnMCPDisabled(client *pluginsdk.Client, handler func()) {
	client.Events().On(MCPServerDisabled, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		handler()
	}))
}

// OnMCPToolsChanged subscribes to MCP tool list changes.
func OnMCPToolsChanged(client *pluginsdk.Client, handler func()) {
	client.Events().On(MCPToolsChanged, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		handler()
	}))
}

// OnRelayProgress subscribes to relay progress events with a typed payload.
func OnRelayProgress(client *pluginsdk.Client, handler func(RelayProgressPayload)) {
	client.Events().On(RelayProgress, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p RelayProgressPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", RelayProgress, err)
			return
		}
		handler(p)
	}))
}

// OnIngressReady subscribes to ingress tunnel readiness with a typed payload.
func OnIngressReady(client *pluginsdk.Client, handler func(IngressReadyPayload)) {
	client.Events().On(IngressReady, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p IngressReadyPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", IngressReady, err)
			return
		}
		handler(p)
	}))
}

// OnWebhookReady subscribes to webhook readiness with a typed payload.
func OnWebhookReady(client *pluginsdk.Client, handler func(WebhookReadyPayload)) {
	client.Events().On(WebhookReady, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p WebhookReadyPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", WebhookReady, err)
			return
		}
		handler(p)
	}))
}

// OnWebhookPluginURL subscribes to webhook URL assignment with a typed payload.
func OnWebhookPluginURL(client *pluginsdk.Client, handler func(WebhookPluginURLPayload)) {
	client.Events().On(WebhookPluginURL, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p WebhookPluginURLPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", WebhookPluginURL, err)
			return
		}
		handler(p)
	}))
}

// OnUserRegistered subscribes to new user registration with a typed payload.
func OnUserRegistered(client *pluginsdk.Client, handler func(UserPayload)) {
	client.Events().On(UserRegistered, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p UserPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", UserRegistered, err)
			return
		}
		handler(p)
	}))
}

// OnSchedulerFired subscribes to scheduled job execution with a typed payload.
func OnSchedulerFired(client *pluginsdk.Client, handler func(SchedulerFiredPayload)) {
	client.Events().On(SchedulerFired, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p SchedulerFiredPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", SchedulerFired, err)
			return
		}
		handler(p)
	}))
}

// OnTaskAssign subscribes to task assignment with a typed payload.
func OnTaskAssign(client *pluginsdk.Client, handler func(TaskAssignPayload)) {
	client.Events().On(TaskTrackingAssign, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p TaskAssignPayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", TaskTrackingAssign, err)
			return
		}
		handler(p)
	}))
}

// DebouncedOnAliasRegistryUpdate subscribes with a timed debouncer for coalescing rapid events.
func DebouncedOnAliasRegistryUpdate(client *pluginsdk.Client, debouncer pluginsdk.Debouncer) {
	client.Events().On(AliasRegistryUpdate, debouncer)
}

// DebouncedOnAgentUpdate subscribes with a timed debouncer for coalescing rapid events.
func DebouncedOnAgentUpdate(client *pluginsdk.Client, debouncer pluginsdk.Debouncer) {
	client.Events().On(AgentUpdate, debouncer)
}
