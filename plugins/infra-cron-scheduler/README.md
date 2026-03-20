# infra-cron-scheduler

Cron-style scheduled event system for automated workflows and recurring jobs.

## Overview

An in-memory scheduler that fires events on intervals. Supports one-shot (`once`) and repeating (`repeat`) events with Go duration intervals. Events are managed via REST API and firing history is kept in a capped log. Emits platform events when events are created/updated/deleted.

## Capabilities

- `infra:cron`

## Dependencies

None.

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/events` | Create event (`{name, text, type, interval}`) |
| GET | `/events` | List all events |
| GET | `/events/:id` | Get single event |
| PUT | `/events/:id` | Update event (partial: `{name, text, interval, enabled}`) |
| DELETE | `/events/:id` | Delete event |
| GET | `/log` | Firing log, newest first (supports `?limit=N`, default 50) |

## Events

**Subscribes to:** none

**Emits:**
- `event_created` — when a new event is created (detail: `"name (interval)"`)
- `event_updated` — when an event is modified (detail: event name)
- `event_deleted` — when an event is removed (detail: event ID)

## How It Works

### Scheduler loop

A background goroutine ticks every 1 second. On each tick, it checks all enabled events against `time.Now()`. Due events are fired (logged) and:
- `once` events: disabled after firing
- `repeat` events: `NextFire` is advanced by `Interval` from the current time

### Event model

| Field | Description |
|-------|-------------|
| `id` | UUID |
| `name` | Human-readable name |
| `text` | Payload text emitted on fire |
| `type` | `once` or `repeat` |
| `interval` | Go duration (`30s`, `5m`, `1h`, `24h`) |
| `next_fire` | When the event will next fire |
| `enabled` | Can be toggled on/off |
| `fired_count` | How many times the event has fired |

### Firing log

Each firing creates a `LogEntry` with the event ID, name, text, and timestamp. The log is capped at 1000 entries (oldest dropped). Accessible via `GET /log`.

### Re-enabling behavior

If a disabled event is re-enabled and its `NextFire` is in the past, it's automatically rearmed to fire after one interval from now.

## Gotchas / Notes

- All state is in-memory — restarts lose all events and logs. There is no database.
- The scheduler currently only logs firings locally — it does not deliver the event text to any target (no HTTP callback, no relay integration). The `text` field and log exist, but actual delivery/action on fire is not implemented.
- Minimum interval is 1 second.
- The 1-second tick resolution means events can fire up to ~1 second late.
- `interval` in the JSON response is `interval_ns` (nanoseconds as a number) plus `interval` (human-readable string).
