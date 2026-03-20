# infra-alias-registry

Central alias registry — manages agents, tool aliases, and personas with persistent storage.

## Overview

The single source of truth for all addressable names in the platform. Stores alias definitions (name, type, target plugin, model, system prompt) in SQLite and broadcasts changes via `alias:update` events. The relay and MCP server subscribe to these events for live updates. Also provides a backward-compatible persona endpoint for the relay.

## Capabilities

- `tool:aliases`

## Dependencies

None.

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_PORT` | number | `8090` | HTTP port |
| `PLUGIN_DATA_PATH` | string | `/data` | Path to store the SQLite database |
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/alias/:name` | Get a single alias by name |
| GET | `/aliases` | List all aliases (supports `?type=agent\|tool_agent\|tool`) |
| POST | `/aliases` | Create alias (`{name, type, plugin, provider, model, system_prompt}`) |
| PUT | `/aliases/:name` | Update alias (partial update, all fields optional except name in path) |
| DELETE | `/aliases/:name` | Delete alias |
| GET | `/persona/:alias` | Backward-compatible persona lookup (returns `{alias, system_prompt, backend_alias, model}`) |
| GET | `/tools` | MCP tool discovery |
| POST | `/mcp/list_aliases` | MCP: list all aliases |
| POST | `/mcp/get_alias` | MCP: get alias by name |
| POST | `/mcp/create_alias` | MCP: create alias |
| POST | `/mcp/update_alias` | MCP: update alias |
| POST | `/mcp/delete_alias` | MCP: delete alias |
| POST | `/mcp/migrate_from_kernel` | MCP: import aliases from kernel |
| POST | `/migrate-from-kernel` | REST: import aliases from kernel (skips existing) |

## Events

- **Subscribes to:** none
- **Emits:** `alias:update` — broadcast after every create, update, delete, or migration. Payload is `{"aliases": [...]}` with the full alias list.

## How It Works

### Alias types

| Type | Description | Example |
|------|-------------|---------|
| `agent` | Full persona: provider plugin + model + system prompt | `@coder` -> `agent-claude` |
| `tool_agent` | AI-powered tool plugin | `@nanobanana` -> `tool-nanobanana` |
| `tool` | Service plugin | `@storage` -> `storage-sss3` |

### Broadcast mechanism

Every mutation (create/update/delete) calls `broadcastAliasUpdate()` which:
1. Reads the full alias list from the database
2. JSON-marshals it
3. Emits an `alias:update` event via the SDK

The relay and MCP server subscribe to this event and rebuild their internal alias maps.

### Kernel migration

`POST /migrate-from-kernel` fetches aliases from the kernel's alias API via `sdk.FetchAliases()`, converts them to registry format (inferring type from plugin name prefix: `agent-*` = agent, `tool-*` = tool_agent, else tool), and inserts them. Existing aliases are skipped (not overwritten).

### Persona compatibility

`GET /persona/:alias` returns the alias data reshaped into the persona format the relay expects: `{alias, system_prompt, backend_alias, model}`. The `backend_alias` field maps to the `provider` column.

## Gotchas / Notes

- The `provider` field on agent-type aliases is the backend plugin ID (e.g. `agent-claude`), not a human-readable name.
- The MCP endpoints return 200 with `{"error": "..."}` for not-found cases instead of 404, matching MCP tool call conventions.
- `PUT /aliases/:name` uses pointer fields for partial updates on the REST API, but the MCP `update_alias` endpoint replaces empty strings (provider, model, system_prompt) with blank values — be careful with MCP updates.
- Database is `aliases.db` in the configured data path.
