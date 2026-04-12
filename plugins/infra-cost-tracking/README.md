# infra-cost-tracking

Centralized usage tracking for all AI agent and tool plugins. Collects token usage and request records into a SQLite database and provides summary analytics.

## How It Works

### Dual Ingestion Paths

1. **Direct POST** (`POST /usage`) -- plugins call this endpoint directly with a usage record.
2. **Event-driven** (`POST /events/usage`) -- plugins emit `usage:report` events via `sdk.ReportAddressedEvent`, and the kernel delivers them here. Malformed payloads return 200 to prevent infinite retry.

### Usage Record Fields

| Field | Description |
|-------|-------------|
| `plugin_id` | Source plugin (e.g. `agent-anthropic`) |
| `provider` | API provider (e.g. `anthropic`, `openai`) |
| `model` | Model used |
| `record_type` | `token` (default) or `request` |
| `input_tokens` / `output_tokens` / `total_tokens` | Token counts |
| `cached_tokens` / `reasoning_tokens` | Extended token tracking |
| `duration_ms` | Request duration |
| `user_id` | Originating user (for per-user analytics) |
| `backend` | Backend type (e.g. `api_key`, `cli`) |
| `status` | For request-type: `submitted`, `completed`, `failed` |
| `prompt` / `task_id` | For video/image tools: generation prompt and task ID |

### Summary Endpoint

`GET /usage` returns: `total_records`, `today_records` (since midnight UTC), `week_records` (last 7 days), and `models` (breakdown by provider+model sorted by most used).

## Capabilities

- `cost:tracking`

## Configuration

No plugin-specific config fields. Uses standard `PLUGIN_DEBUG` from kernel config.

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/usage` | Direct usage report |
| GET | `/usage` | Aggregate summary (supports `?user_id=`) |
| GET | `/usage/records` | List all records (supports `?since=RFC3339&user_id=`) |
| GET | `/usage/users` | Distinct user IDs with record counts |
| POST | `/events/usage` | Webhook for kernel-delivered `usage:report` events |

## Events

**Subscribes to:** `usage:report` via kernel addressed delivery (retry loop, up to 30 attempts at startup)

**Emits:** none

## Notes

- Database: `costs.db` in `/data/`.
- No cost-in-dollars calculation -- tracks raw usage metrics only.
- The `reasoning_tokens` field is not populated via the event path (only via direct POST).
