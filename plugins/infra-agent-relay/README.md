# infra-agent-relay

Central message routing hub for all chat messages. Messaging plugins (Discord, Telegram, WhatsApp, web chat) send messages here; the relay resolves the target agent via persona lookup, injects conversation history and semantic memory, streams the response back via progress events, and exposes a `chat_to_agent` tool for inter-agent delegation.

## Capabilities

- `infra:agent-relay`
- `tool:agent-delegation`

## Dependencies

- `tool:memory` -- conversation history + semantic memory (optional, degrades gracefully)
- `tool:aliases` -- alias registry for agent resolution (optional)
- `tool:personas` -- persona definitions with system prompts (discovered dynamically)

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ORCHESTRATION_MODE` | select | `direct` | `direct` = single agent with tools (streaming, lower latency). `coordinator` = DAG orchestration for multi-agent planning. |
| `MAX_ORCHESTRATION_TASKS` | number | `20` | Max tasks in a coordinator's DAG plan (coordinator mode only) |
| `TASK_TIMEOUT_SECONDS` | number | `120` | Per-task deadline before failure |
| `PLUGIN_DEBUG` | boolean | `false` | Verbose logging + debug trace DB at `/data/relay_traces.db` |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/schema` | Plugin schema (includes route map) |
| POST | `/events` | SDK event handler |
| POST | `/chat` | Main entry point: `{source_plugin, channel_id, message, image_urls}` |
| GET | `/mcp` | Tool definitions (chat_to_agent) |
| POST | `/tools/chat_to_agent` | Delegate a message to another agent by alias |
| POST | `/config/workspace/map` | Map channel to workspace bridge |
| POST | `/config/workspace/unmap` | Remove channel-workspace mapping |
| GET | `/status` | Current routing state: aliases, workspace mappings |
| GET | `/debug/traces` | Debug trace list (when debug enabled) |
| GET | `/debug/traces/:request_id` | Debug trace detail |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias-registry:update` | Patches alias map on create/update/delete (no debounce) |
| `alias-registry:ready` | Full alias refresh (1s debounce) |
| `persona:update` | Refresh persona cache (1s debounce) |
| `config:update` | Hot-reload timeout, debug, orchestration settings |
| `relay:task:progress` | Forward async plugin progress to messaging plugins, resolve async waiters |

**Emits:**

| Event | Description |
|-------|-------------|
| `relay:progress` | Task status updates to source messaging plugin (addressed) |

## How It Works

### Message routing (POST /chat)

Returns `{task_group_id}` immediately (HTTP 202). All results delivered via `relay:progress` events.

1. **Workspace bridge** -- if channel is mapped to a workspace, forward over TCP (binary framing protocol, 16MB max).
2. **@alias direct routing** -- if message starts with `@alias`, resolve via persona lookup and route directly to that agent. No persona = no chat (bare aliases are infrastructure-only).
3. **Default agent** -- if no @alias, resolve the persona marked `is_default` and route to it.

### Persona injection

For all agent calls, the relay looks up the persona's `system_prompt` and injects it into the request. The system prompt is enriched with available agent aliases (for `chat_to_agent` tool awareness).

### Memory integration

- Fetches conversation history from `infra-agent-memory-gateway` using session ID `source_plugin:channel_id`
- Searches semantic memory (Mem0) for relevant facts, injected into system prompt
- Stores user + assistant messages (fire-and-forget)
- Extracts facts from conversations for long-term memory (fire-and-forget)

### Streaming

Agent responses are streamed via SSE from agent plugins. The relay emits `relay:progress` events with status `streaming` as chunks arrive, then `completed` with the full response.

### Inter-agent delegation (chat_to_agent)

Exposed as an MCP tool. Agents can delegate tasks to other agents by alias. Recursion depth capped at 3 to prevent infinite loops.

### Async task handling

Some agent tools (e.g. video generation) return a `{status: "processing", task_id: "..."}` response. The relay registers an async waiter and blocks until a `relay:task:progress` event resolves it.

## Notes

- Persona cache loaded on startup and refreshed reactively via `persona:update` events. No polling.
- Memory plugin discovery cached for 60 seconds.
- Debug traces stored in SQLite at `/data/relay_traces.db` when `PLUGIN_DEBUG` is enabled.
- Uses Gin for HTTP routing.
