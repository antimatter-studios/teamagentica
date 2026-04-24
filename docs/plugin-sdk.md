# Plugin SDK

> Go SDK for building TeamAgentica plugins with automatic registration, heartbeats, events, and graceful shutdown.

## Overview

The Plugin SDK (`pkg/pluginsdk`) provides everything a plugin needs to integrate with the kernel. It handles the full lifecycle: registration, heartbeats, event subscriptions, alias management, storage access, and graceful shutdown. Plugins focus on business logic while the SDK manages all kernel communication.

## Quick Start

```go
package main

import (
    "context"
    "net/http"

    "github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
    "github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

func main() {
    cfg := pluginsdk.LoadConfig()

    reg := pluginsdk.Registration{
        ID:           "my-plugin",
        Host:         "my-plugin",
        Port:         8081,
        Capabilities: []string{"custom:capability"},
        Version:      "1.0.0",
        ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
            "API_KEY": {Type: "string", Label: "API Key", Required: true, Secret: true},
        },
    }

    client := pluginsdk.NewClient(cfg, reg)

    // Register event handlers BEFORE Start()
    client.OnEvent("config:updated", pluginsdk.NewNullDebouncer(func(e pluginsdk.EventCallback) {
        // handle config change
    }))

    client.Start(context.Background())

    // Fetch aliases
    infos, _ := client.FetchAliases()
    aliasMap := alias.NewAliasMap(infos)

    // Set up HTTP server
    server := &http.Server{Addr: ":8081", Handler: myRouter}
    pluginsdk.RunWithGracefulShutdown(server, client) // blocks until SIGINT/SIGTERM
}
```

## Configuration

`LoadConfig()` reads these environment variables (injected by the kernel at container start):

| Variable | Purpose |
|----------|---------|
| `TEAMAGENTICA_KERNEL_HOST` | Kernel hostname |
| `TEAMAGENTICA_KERNEL_PORT` | Kernel port |
| `TEAMAGENTICA_PLUGIN_ID` | This plugin's ID |
| `TEAMAGENTICA_PLUGIN_TOKEN` | Bearer token for kernel auth |
| `TEAMAGENTICA_TLS_CERT` | Client cert PEM path |
| `TEAMAGENTICA_TLS_KEY` | Client key PEM path |
| `TEAMAGENTICA_TLS_CA` | CA cert PEM path |
| `TEAMAGENTICA_TLS_ENABLED` | Enable mTLS (`"true"`) |

## Registration

Plugins describe themselves via `Registration`:

```go
type Registration struct {
    ID           string                             `json:"id"`
    Host         string                             `json:"host"`
    Port         int                                `json:"port"`
    EventPort    int                                `json:"event_port,omitempty"`
    Capabilities []string                           `json:"capabilities"`
    Version      string                             `json:"version"`
    ConfigSchema map[string]ConfigSchemaField        `json:"config_schema,omitempty"`
    Schema       map[string]any                      `json:"schema,omitempty"` // arbitrary plugin metadata schema
}
```

### Config Schema Fields

Config schema defines what appears in the plugin settings UI:

```go
type ConfigSchemaField struct {
    Type        string       `json:"type"`          // "string", "boolean", "select", "aliases"
    Label       string       `json:"label"`
    Required    bool         `json:"required,omitempty"`
    Secret      bool         `json:"secret,omitempty"`     // masked in UI
    ReadOnly    bool         `json:"readonly,omitempty"`
    Default     string       `json:"default,omitempty"`
    Options     []string     `json:"options,omitempty"`    // for select type
    Dynamic     bool         `json:"dynamic,omitempty"`    // options fetched from /config/options/:field
    HelpText    string       `json:"help_text,omitempty"`
    VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`
    Order       int          `json:"order,omitempty"`
}

type VisibleWhen struct {
    Field string `json:"field"` // controlling field name
    Value string `json:"value"` // value that makes this field visible
}
```

## Client Lifecycle

### Start

`client.Start(ctx)` is non-blocking. It:

1. Starts an internal event server (if handlers registered via `OnEvent`)
2. Registers with the kernel (`POST /api/plugins/register`) with exponential backoff (1s→30s cap)
3. Subscribes to all event types registered via `OnEvent`
4. Runs heartbeats every 30 seconds (`POST /api/plugins/heartbeat`)

### Stop

`client.Stop()` is blocking. It:

1. Stops all registered debouncers
2. Shuts down the internal event server (2s timeout)
3. Deregisters from the kernel (`POST /api/plugins/deregister`)

### Graceful Shutdown

```go
pluginsdk.RunWithGracefulShutdown(server, client)
```

Starts the HTTP server (with mTLS if configured), waits for `SIGINT`/`SIGTERM`, then calls `client.Stop()` and `server.Shutdown()` (10s timeout).

## Event System

### Types

```go
type EventCallback struct {
    EventType string `json:"event_type"`
    PluginID  string `json:"plugin_id"`
    Detail    string `json:"detail"`
    Timestamp string `json:"timestamp"`
    Seq       uint64 `json:"seq,omitempty"` // monotonic sequence for stale detection
}

type EventHandler func(event EventCallback)
```

### Subscribing

Register handlers before `Start()`:

```go
client.OnEvent("config:updated", pluginsdk.NewNullDebouncer(handler))
client.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, handler))
```

Manual subscription (for custom callback paths):

```go
client.Subscribe("usage:report", "/events/usage")
client.Unsubscribe("usage:report")
```

### Emitting Events

```go
// Broadcast (fire-and-forget)
client.ReportEvent("chat_request", "model=gpt-4o")

// Addressed (at-least-once delivery)
client.ReportAddressedEvent("webhook:tunnel:update", `{"url":"..."}`, "network-webhook-ingress")

// Usage report (addressed to infra-cost-explorer)
client.ReportUsage(pluginsdk.UsageReport{
    Provider:     "openai",
    Model:        "gpt-4o",
    InputTokens:  1500,
    OutputTokens: 500,
    DurationMs:   2300,
})
```

### UsageReport

```go
type UsageReport struct {
    UserID       string `json:"user_id,omitempty"`
    Provider     string `json:"provider"`
    Model        string `json:"model,omitempty"`
    RecordType   string `json:"record_type,omitempty"`
    Status       string `json:"status,omitempty"`
    Prompt       string `json:"prompt,omitempty"`
    TaskID       string `json:"task_id,omitempty"`
    InputTokens  int    `json:"input_tokens,omitempty"`
    OutputTokens int    `json:"output_tokens,omitempty"`
    TotalTokens  int    `json:"total_tokens,omitempty"`
    CachedTokens int    `json:"cached_tokens,omitempty"`
    DurationMs   int64  `json:"duration_ms,omitempty"`
}
```

## Debouncing

Two strategies for event delivery:

### NullDebouncer

Fires handler immediately and synchronously for every event.

```go
pluginsdk.NewNullDebouncer(handler)
```

### TimedDebouncer

Waits for a quiet period before firing with the latest event. Resets the timer on each new event. Sequence-aware: events with a `Seq` lower than previously seen are silently dropped.

```go
pluginsdk.NewTimedDebouncer(2*time.Second, handler)
```

## Managed Containers

Plugins can create and manage Docker containers through the kernel. Containers are scoped per-plugin — a plugin can only see and manage its own containers.

```go
// Create a managed container
resp, err := client.CreateManagedContainer(pluginsdk.CreateContainerRequest{
    Name:       "ws-a1b2c3d4",
    Image:      "codercom/code-server:latest",
    Port:       8080,
    Subdomain:  "ws-a1b2c3d4",
    VolumeName: "ws-a1b2c3d4-my-project",
    Cmd:        []string{"--auth", "none"},
})

// List all containers owned by this plugin
containers, err := client.ListManagedContainers()

// Get a specific container
container, err := client.GetManagedContainer(id)

// Delete a container (stops it and removes the record)
err := client.DeleteManagedContainer(id)

// Update container metadata
err := client.UpdateManagedContainer(id, pluginsdk.UpdateContainerRequest{
    Name:       strPtr("new-name"),
    VolumeName: strPtr("new-volume-name"),
})
```

### CreateContainerRequest

| Field | Type | Description |
|-------|------|-------------|
| `Name` | `string` | Container display name |
| `Image` | `string` | Docker image reference |
| `Port` | `int` | Container port to expose |
| `Subdomain` | `string` | Subdomain for kernel proxy routing |
| `VolumeName` | `string` | Docker volume for persistent data |
| `Cmd` | `[]string` | Container command override |
| `Labels` | `map[string]string` | Docker labels |
| `Env` | `map[string]string` | Environment variables |

## Plugin Discovery & Routing

```go
// Find plugins by capability prefix
plugins, err := client.SearchPlugins("agent:tool:image")
// Returns []PluginInfo{{ID: "agent-stability", Capabilities: [...]}, ...}

// Proxy request to another plugin via kernel
resp, err := client.RouteToPlugin(ctx, "agent-stability", "POST", "/generate", body)
```

## Storage Helpers

Access S3-compatible storage through the kernel (auto-discovers `storage:api` plugin):

```go
err := client.StorageWrite(ctx, "media/image.png", reader, "image/png")

body, contentType, err := client.StorageRead(ctx, "media/image.png")
defer body.Close()

err = client.StorageDelete(ctx, "media/image.png")

list, err := client.StorageList(ctx, "media/")
// Returns *StorageListResult{Objects: [...], Count: N}
```

## Alias Integration

The `alias` sub-package manages `@mention` routing:

```go
import "github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"

// Fetch from kernel and build map
infos, _ := client.FetchAliases()
aliasMap := alias.NewAliasMap(infos)

// Parse user message
result := aliasMap.Parse("@claude write a poem")
// result.Alias = "claude", result.Target = &Target{PluginID: "agent-openai"}, result.Remainder = "write a poem"

// Direct lookup
target := aliasMap.Resolve("claude")

// Reverse lookup (plugin ID → alias name)
name := aliasMap.FindAliasByPluginID("agent-openai") // "claude"

// Hot-swap on alias update event
aliasMap.Replace(newInfos)

// Generate coordinator system prompt
prompt := aliasMap.SystemPromptBlock()
promptWithTools := aliasMap.SystemPromptBlockWithTools(discoveredTools)
```

### Target Types

```go
const (
    TargetAgent TargetType = iota  // agent:chat plugins
    TargetImage                    // agent:tool:image plugins
    TargetVideo                    // agent:tool:video plugins
)
```

### Coordinator Response Parsing

```go
alias, message, isDelegation := alias.ParseCoordinatorResponse(response)
// Parses "ROUTE:@claude\nwrite a poem" → ("claude", "write a poem", true)
```

## TLS Helpers

```go
// Server-side mTLS config (for plugin HTTP servers)
tlsConfig, err := pluginsdk.GetServerTLSConfig(cfg)

// Client-side TLS config (for outbound requests)
tlsConfig := client.TLSConfig()
```

## Common Patterns

### AI Agent Plugin

All agent plugins follow this pattern:
- Capabilities: `["agent:chat", "agent:chat:<provider>"]`
- Endpoints: `/health`, `/chat`, `/models`, `/config/options/:field`, `/usage`, `/usage/records`, `/pricing`
- Events: emit `chat_request`, `chat_response`, `error` via `ReportEvent`; emit usage via `ReportUsage`

### Tool Plugin

- Capabilities: `["agent:tool:image", "agent:tool:image:<provider>"]` or `["agent:tool:video", ...]`
- Endpoints: `/health`, `/generate`, `/pricing`, `/usage`
- Sync tools return result immediately; async tools return `task_id` with a `/status/:taskId` poll endpoint

### Workspace Environment Plugin

Plugins that manage development environments using managed containers:
- Capabilities: `["workspace:manager"]`
- Endpoints: `/health`, `/workspaces` (CRUD), `/workspaces/:id/status`
- Uses `CreateManagedContainer` to spawn VS Code Server (code-server) instances
- Each workspace gets a persistent Docker volume for project files
- Kernel assigns subdomains and proxies traffic to workspace containers
- Workspaces survive plugin restarts — kernel tracks containers independently

```go
// Create a workspace
resp, err := client.CreateManagedContainer(pluginsdk.CreateContainerRequest{
    Name:       "ws-" + shortID,
    Image:      "codercom/code-server:latest",
    Port:       8080,
    Subdomain:  "ws-" + shortID,
    VolumeName: "ws-" + shortID + "-" + projectSlug,
    Cmd:        []string{"--auth", "none"},
})
```

### Messaging Plugin

- Capabilities: `["messaging:<platform>", "messaging:send", "messaging:receive"]`
- Subscribe to: `kernel:alias:update`, `config:update`
- Route messages via alias parsing + coordinator delegation
- Buffer sequential messages with configurable debounce (`MESSAGE_BUFFER_MS`, default 1000ms)
- Chat requests include `isCoordinator` flag and `agentAlias` for agent-side system prompt construction
- Extract media from attachments, embeds, forwarded messages, and reply-to-message chains
- Attribute responses with `[@alias]` prefix showing which agent replied

## Related

- [Kernel](kernel.md) — API reference and event system details
- [Architecture](architecture.md) — System architecture overview
