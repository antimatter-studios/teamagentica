# infra-alias-registry

Central alias registry -- single source of truth for all addressable names (agents, tool agents, tools) in the platform. Stores alias definitions in SQLite and broadcasts changes via events so the relay and MCP server stay in sync.

## How It Works

Each alias maps a name to a target plugin with optional model and system prompt. Mutations (create/update/delete) broadcast `alias-registry:update` events. The relay and MCP server subscribe to these for live updates.

### Alias Types

| Type | Description | Example |
|------|-------------|---------|
| `agent` | Full persona: provider plugin + model + system prompt | `coder` -> `agent-anthropic` |
| `tool_agent` | AI-powered tool plugin | `nanobanana` -> `tool-nanobanana` |
| `tool` | Service plugin | `storage` -> `storage-sss3` |

### Persona Compatibility

`GET /persona/:alias` returns alias data in the shape the relay expects: `{alias, system_prompt, backend_alias, model}`.

### Kernel Migration

`POST /migrate-from-kernel` imports aliases from the kernel's alias API, inferring type from plugin name prefix (`agent-*` = agent, `tool-*` = tool_agent, else tool). Existing aliases are skipped.

## Capabilities

- `tool:aliases`
- `alias:manage`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/alias/:name` | Get single alias by name |
| GET | `/aliases` | List all aliases (supports `?type=agent\|tool_agent\|tool`) |
| POST | `/aliases` | Create alias |
| PUT | `/aliases/:name` | Update alias (partial update via pointer fields) |
| DELETE | `/aliases/:name` | Delete alias (soft delete) |
| GET | `/persona/:alias` | Backward-compatible persona lookup for relay |
| GET | `/mcp` | MCP tool discovery |
| POST | `/mcp/list_aliases` | MCP: list all aliases |
| POST | `/mcp/get_alias` | MCP: get alias by name |
| POST | `/mcp/create_alias` | MCP: create alias |
| POST | `/mcp/update_alias` | MCP: update alias |
| POST | `/mcp/delete_alias` | MCP: delete alias |
| POST | `/mcp/migrate_from_kernel` | MCP: import aliases from kernel |
| POST | `/migrate-from-kernel` | REST: import aliases from kernel |

## Events

**Subscribes to:** none

**Emits:**
- `alias-registry:update` -- after every create, update, delete, or migration. Payload: `{action, alias}`.
- `alias-registry:ready` -- on startup, so plugins that started before this one can re-fetch.

## Notes

- Database: `aliases.db` in `/data/`.
- Uses GORM soft deletes with a partial unique index on `(name) WHERE deleted_at IS NULL`.
- Alias names are sanitized: lowercased, `@` stripped, non-alphanumeric chars removed.
- MCP not-found cases return 200 with `{"error": "..."}` (MCP convention).
- REST `PUT` uses pointer fields for true partial updates; MCP `update_alias` replaces empty strings with blank values.
- Pushes tool definitions to MCP server on availability via `RegisterToolsWithMCP`.
