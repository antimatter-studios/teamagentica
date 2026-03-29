# infra-agent-relay

Routes messages between messaging plugins and AI agents, with DAG-based multi-agent orchestration.

## Overview

The relay is the central routing hub for all chat messages. Messaging plugins (Discord, Telegram, WhatsApp, etc.) send messages here; the relay resolves which agent should handle them, optionally runs a coordinator that produces a DAG execution plan, executes the plan with parallel task execution, and returns the final response. It also integrates with infra-agent-memory-gateway for conversation history and infra-alias-registry for persona system prompts.

## Capabilities

- `infra:agent-relay`

## Dependencies

- `tool:memory` — conversation history (optional, degrades gracefully)
- `tool:aliases` — persona definitions and alias registry (optional)

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DEFAULT_COORDINATOR` | select (dynamic) | _(unset)_ | Fallback coordinator alias. If unset, the first `relay:coordinator` event becomes the default. |
| `MAX_ORCHESTRATION_TASKS` | number | `20` | Maximum tasks allowed in a coordinator's DAG plan |
| `TASK_TIMEOUT_SECONDS` | number | `120` | Per-task deadline before failure |
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/chat` | Main entry point — accepts `{source_plugin, channel_id, message, image_urls}` |
| POST | `/config/coordinator` | Set coordinator for a source plugin (`{source_plugin, plugin_id/alias, model}`) |
| POST | `/config/workspace/map` | Map a channel to a workspace bridge (`{source_plugin, channel_id, workspace_id, bridge_addr}`) |
| POST | `/config/workspace/unmap` | Remove channel-to-workspace mapping |
| GET | `/status` | Current routing state: connections, coordinators, workspace mappings |

## Events

**Subscribes to:**
- `alias:update` — rebuilds the alias map from infra-alias-registry (2s debounce)
- `relay:coordinator` — sets a coordinator for a source plugin via event
- `config:update` — hot-reloads `DEFAULT_COORDINATOR`, `MAX_ORCHESTRATION_TASKS`, `TASK_TIMEOUT_SECONDS`

**Emits:**
- `relay:ready` — fired 500ms after startup

## How It Works

### Message routing priority

1. **Workspace bridge** — if the channel is mapped to a workspace, the message is forwarded over TCP using a custom binary framing protocol.
2. **@alias direct routing** — if the message starts with `@alias`, it bypasses orchestration and routes directly to that agent.
3. **Coordinator + DAG orchestration** — the coordinator agent is called in orchestration mode. If it returns a JSON DAG plan, the relay executes it; if it returns plain text, that's the final answer.

### DAG orchestration

The coordinator returns a `{"tasks": [...]}` JSON plan where each task has `{id, alias, prompt, depends_on}`. The relay:
- Validates task count against `MAX_ORCHESTRATION_TASKS`
- Checks for circular dependencies via DFS cycle detection
- Executes tasks in topological waves — tasks with satisfied dependencies run in parallel
- Substitutes `{taskID}` placeholders in prompts with completed task results
- Tasks with `alias: "self"` route back to the coordinator for synthesis
- Terminal tasks (nothing depends on them) produce the final output

### Persona injection

For non-coordinator (worker) agent calls, the relay looks up the agent's alias in the persona cache. If a persona exists with a `system_prompt`, it's injected into the `agentChatRequest.SystemPrompt` field.

### Memory integration

- Before routing, fetches conversation history from infra-agent-memory-gateway using session ID `source_plugin:channel_id`
- Stores incoming user messages and outgoing assistant messages (fire-and-forget)
- Memory plugin discovery is cached for 60 seconds

### Alias merging

Kernel aliases are merged with persona aliases from infra-alias-registry. Personas with a `backend_alias` become routable agent entries; the model can be overridden per-persona.

## Gotchas / Notes

- The `allowFirstAsDefault` flag causes the first `relay:coordinator` event to auto-set the default coordinator when `DEFAULT_COORDINATOR` is unset. This is a one-shot behavior.
- Workspace bridge uses a custom binary TCP protocol (`bridge` package) with 16MB max payload, not HTTP.
- The persona cache has a 60-second TTL — changes to personas in the alias registry take up to 60s to take effect in the relay.
- The coordinator map is exposed via `GET /schema` for the TUI to display which messaging plugin maps to which coordinator.
- If multiple terminal tasks exist and no synthesis task was included, results are concatenated as a fallback.
