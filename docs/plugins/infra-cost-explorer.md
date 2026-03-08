# infra-cost-explorer

> AI usage tracking and cost analytics across all agent plugins.

## Overview

The infra-cost-explorer plugin aggregates usage data from all AI agent plugins. It receives usage reports via the kernel's addressed event system and provides APIs for querying costs by time period, model, and user. This is a system plugin that cannot be disabled.

## Capabilities

- `system:cost-explorer` — Cost tracking and analytics

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `PLUGIN_PORT` | int | no | `8090` | HTTP port |
| `PLUGIN_DATA_PATH` | string | no | `/data` | Data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/usage` | Direct usage ingestion |
| `GET` | `/usage` | Aggregate summary (`?user_id=` filter) |
| `GET` | `/usage/records` | Raw records (`?since=RFC3339`, `?user_id=` filters) |
| `GET` | `/usage/users` | Distinct user IDs with record counts |
| `POST` | `/events/usage` | Kernel event delivery endpoint |

## Events

### Subscriptions

- `usage:report` — Receives usage data from agent plugins (retry up to 30x at 2s intervals)

## Usage

Agent plugins emit usage reports via `sdk.ReportUsage()`, which are addressed to `infra-cost-explorer`. Each report includes:

- Provider and model name
- Input, output, total, and cached token counts
- Request duration
- User ID (when available)
- Task ID for correlation

### Summary Response

`GET /usage` returns:
- `total_records` — All-time count
- `today_records` — Today's count
- `week_records` — Last 7 days
- `models[]` — Per-model breakdown

### Database

SQLite at `$PLUGIN_DATA_PATH/costs.db` with `UsageRecord` table tracking: plugin_id, provider, model, record_type (`token`/`request`), all token fields, duration, user_id, backend, status, prompt, task_id.

## Related

- [Kernel](../kernel.md) — Event system details
- [Plugin SDK](../plugin-sdk.md) — `ReportUsage()` API
