# infra-agent-memory

Persistent conversation memory for agents — stores session history and provides a swappable memory backend.

## Overview

Stores conversation messages (user + assistant turns) in per-session SQLite tables. The relay fetches history from here before calling agents, giving them multi-turn context. Also exposes MCP tools so agents can directly query/manage sessions.

## Capabilities

- `tool:memory`

## Dependencies

None.

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_PORT` | number | `8091` | HTTP port |
| `PLUGIN_DATA_PATH` | string | `/data` | Path to store the SQLite database |
| `MAX_SESSION_MESSAGES` | number | `50` | Max messages retained per session before pruning oldest |
| `SESSION_TTL_HOURS` | number | `24` | Hours of inactivity before a session is expired and pruned |
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/sessions` | List all sessions with message counts and last activity |
| GET | `/sessions/:id/messages` | Get message history for a session (supports `?limit=N`) |
| POST | `/sessions/:id/messages` | Append a message (`{role, content, responder}`) |
| DELETE | `/sessions/:id` | Clear all messages for a session |
| GET | `/tools` | MCP tool discovery |
| POST | `/mcp/list_sessions` | MCP: list sessions |
| POST | `/mcp/get_history` | MCP: get history (`{session_id, limit}`) |
| POST | `/mcp/add_message` | MCP: add message (`{session_id, role, content, responder}`) |
| POST | `/mcp/clear_session` | MCP: clear session (`{session_id}`) |

## Events

- **Subscribes to:** none
- **Emits:** none

## How It Works

- Storage is a single SQLite database (`memory.db`) using GORM with a `messages` table indexed on `(session_id, created_at)`.
- Session IDs are opaque strings — the relay uses `source_plugin:channel_id` as the key.
- `GetHistory` returns the most recent N messages ordered oldest-first (sliding window).
- After every `AddMessage`, the session is pruned to `MAX_SESSION_MESSAGES` by deleting the oldest messages.
- A background goroutine runs every hour to prune sessions with no activity in `SESSION_TTL_HOURS`.
- Messages track a `responder` field (agent alias) for assistant messages, enabling attribution.

## Gotchas / Notes

- The MCP `get_history` endpoint defaults to 20 messages, while the REST endpoint defaults to the configured `MAX_SESSION_MESSAGES`.
- Role is validated to be exactly `"user"` or `"assistant"` on the REST endpoint but not on the MCP endpoint.
- Pruning is eventual — the hourly TTL check can leave expired sessions for up to 59 minutes after expiry.
