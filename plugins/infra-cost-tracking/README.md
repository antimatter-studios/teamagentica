# infra-cost-tracking

Centralized usage tracking and cost analytics for all AI agent and tool plugins.

## Overview

Collects token usage and request records from all plugins (agents, image generators, video tools) into a single SQLite database. Plugins report usage either via direct POST or by emitting `usage:report` events that the kernel delivers to this plugin's webhook endpoint. Provides summary analytics with model breakdowns and per-user filtering.

## Capabilities

- `cost:tracking`

## Dependencies

None.

## Configuration

No plugin-specific config fields beyond the standard `PLUGIN_PORT`, `PLUGIN_DATA_PATH`, and `PLUGIN_DEBUG` (read from kernel config at startup).

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/usage` | Direct usage report (`{plugin_id, provider, model, ...}`) |
| GET | `/usage` | Aggregate summary (supports `?user_id=`) |
| GET | `/usage/records` | List all records (supports `?since=RFC3339&user_id=`) |
| GET | `/usage/users` | Distinct user IDs with record counts |
| POST | `/events/usage` | Webhook for kernel-delivered `usage:report` events |

## Events

**Subscribes to:**
- `usage:report` — via kernel addressed delivery (`sdk.Subscribe`). The kernel POSTs events to `/events/usage`.

**Emits:** none

## How It Works

### Dual ingestion paths

1. **Direct POST** (`/usage`) — plugins call this endpoint directly with a usage record.
2. **Event-driven** (`/events/usage`) — plugins emit `usage:report` events via `sdk.ReportAddressedEvent`, and the kernel delivers them to cost-tracking's webhook. The event envelope contains `{event_type, plugin_id, detail, timestamp}` where `detail` is the JSON usage report.

### Usage record fields

| Field | Description |
|-------|-------------|
| `plugin_id` | Source plugin (e.g. `agent-claude`) |
| `provider` | API provider (e.g. `anthropic`, `openai`) |
| `model` | Model used (e.g. `claude-sonnet-4-20250514`) |
| `record_type` | `token` (default) or `request` |
| `input_tokens` / `output_tokens` / `total_tokens` | Token counts |
| `cached_tokens` / `reasoning_tokens` | Extended token tracking |
| `duration_ms` | Request duration |
| `user_id` | Originating user (for per-user analytics) |
| `backend` | Backend type (e.g. `api_key`, `cli`) |
| `status` | For request-type: `submitted`, `completed`, `failed` |
| `prompt` / `task_id` | For video/image tools: the generation prompt and task ID |

### Summary endpoint

`GET /usage` returns:
- `total_records` — all-time count
- `today_records` — since midnight UTC
- `week_records` — last 7 days
- `models` — breakdown by provider+model with counts, sorted by most used

## Gotchas / Notes

- The event subscription uses a retry loop (up to 30 attempts, 2s apart) because the kernel may not be ready at startup.
- Malformed event payloads return 200 OK to prevent infinite retry — they're logged and skipped.
- Database is `costs.db` in the data path.
- No cost-in-dollars calculation — this tracks raw usage metrics, not monetary costs despite the plugin name.
- The `reasoning_tokens` field exists in the DB schema but is not populated via the event path (only via direct POST).
