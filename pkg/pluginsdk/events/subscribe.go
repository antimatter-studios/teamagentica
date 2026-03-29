package events

import (
	"encoding/json"
	"log"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// OnAliasRegistryUpdate subscribes to alias registry changes with a typed payload.
func OnAliasRegistryUpdate(client *pluginsdk.Client, handler func(AliasRegistryUpdatePayload)) {
	client.OnEvent(AliasRegistryUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(AliasUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p AliasEntry
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", AliasUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnPersonaUpdate subscribes to persona changes with a typed payload.
func OnPersonaUpdate(client *pluginsdk.Client, handler func(PersonaUpdatePayload)) {
	client.OnEvent(PersonaUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		var p PersonaUpdatePayload
		if err := json.Unmarshal([]byte(e.Detail), &p); err != nil {
			log.Printf("events: failed to decode %s: %v", PersonaUpdate, err)
			return
		}
		handler(p)
	}))
}

// OnConfigUpdate subscribes to config changes with a typed payload.
func OnConfigUpdate(client *pluginsdk.Client, handler func(ConfigUpdatePayload)) {
	client.OnEvent(ConfigUpdate, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(MCPServerEnabled, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(MCPServerDisabled, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
		handler()
	}))
}

// OnRelayProgress subscribes to relay progress events with a typed payload.
func OnRelayProgress(client *pluginsdk.Client, handler func(RelayProgressPayload)) {
	client.OnEvent(RelayProgress, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(IngressReady, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(WebhookReady, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(WebhookPluginURL, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(UserRegistered, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(SchedulerFired, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(TaskTrackingAssign, pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
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
	client.OnEvent(AliasRegistryUpdate, debouncer)
}

// DebouncedOnPersonaUpdate subscribes with a timed debouncer for coalescing rapid events.
func DebouncedOnPersonaUpdate(client *pluginsdk.Client, debouncer pluginsdk.Debouncer) {
	client.OnEvent(PersonaUpdate, debouncer)
}
