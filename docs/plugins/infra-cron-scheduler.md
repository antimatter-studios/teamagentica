# infra-cron-scheduler

> Cron-style scheduled event system.

## Overview

The infra-cron-scheduler plugin provides a simple in-memory scheduling system for timed events. Supports one-shot and repeating events with configurable intervals. Events are checked every second via a tick loop.

**Note:** All scheduled events are stored in memory and lost on plugin restart.

## Capabilities

- `tool:scheduler` — Scheduled task management

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `SCHEDULER_PORT` | int | no | `8081` | HTTP port |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/events` | Create event: `{name, text, type, interval}` |
| `GET` | `/events` | List all events |
| `GET` | `/events/:id` | Get event |
| `PUT` | `/events/:id` | Update event: `{name?, text?, interval?, enabled?}` |
| `DELETE` | `/events/:id` | Delete event |
| `GET` | `/log?limit=N` | Fire history (default 50, max stored 1000) |

## Events

### Emissions

- `event_created` — When a scheduled event is created
- `event_updated` — When a scheduled event is modified
- `event_deleted` — When a scheduled event is deleted

## Usage

### Create a Repeating Event

```json
POST /events
{
  "name": "hourly-check",
  "text": "Perform health check",
  "type": "repeat",
  "interval": "1h"
}
```

### Create a One-Shot Event

```json
POST /events
{
  "name": "reminder",
  "text": "Send report",
  "type": "once",
  "interval": "30m"
}
```

One-shot events fire once and are automatically disabled.

### Event Types

| Type | Behavior |
|------|----------|
| `once` | Fires once, then sets `enabled = false` |
| `repeat` | Fires every interval, reschedules automatically |

### Intervals

Go duration format: `"5m"`, `"1h"`, `"30s"`, `"24h"`. Minimum: 1 second.

### Re-enabling

Re-enabling a past event arms it from `now + interval`.

## Related

- [Kernel](../kernel.md) — Event system
- [Plugin SDK](../plugin-sdk.md) — SDK reference
