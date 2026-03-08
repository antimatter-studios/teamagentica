# tool-seedance

> Seedance video generation with async polling.

## Overview

The Seedance plugin generates videos using the Seedance API. It uses an asynchronous flow: submit a generation request, then poll for completion. Tasks are stored in memory and lost on restart.

## Capabilities

- `tool:video` — Video generation tool
- `tool:video:seedance` — Seedance-specific

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `SEEDANCE_API_KEY` | string (secret) | yes | — | Seedance API key (Dreamina developer portal) |
| `SEEDANCE_MODEL` | select (dynamic) | no | `seedance-2.0` | Default model |
| `SEEDANCE_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/generate` | Submit video generation (returns `task_id`) |
| `GET` | `/status/:taskId` | Poll generation status |
| `GET` | `/config/options/:field` | Dynamic model options (static list) |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Raw records |
| `GET` | `/pricing` | Get pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Emissions

- `generate_request` — On submission
- `generate_submitted` — After API submission
- `generate_complete` — On completion
- `generate_failed` — On failure
- `error` — On submission failure
- `usage:report` — Addressed to infra-cost-explorer

## Usage

### Generate Video

```json
POST /generate
{
  "prompt": "A drone shot flying over a mountain range",
  "aspect_ratio": "16:9",
  "duration": 4,
  "negative_prompt": "blurry"
}
```

Response (202):
```json
{
  "task_id": "seed-1",
  "status": "processing",
  "message": "Video generation started"
}
```

### Poll Status

```
GET /status/seed-1
```

Response (when complete):
```json
{
  "task_id": "seed-1",
  "status": "completed",
  "video_url": "https://...",
  "model": "seedance-2.0",
  "prompt": "...",
  "created_at": "...",
  "completed_at": "..."
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `prompt` | string | required | Video description |
| `aspect_ratio` | string | `16:9` | Output aspect ratio |
| `duration` | int | `4` | Duration in seconds (4 or 8) |
| `negative_prompt` | string | `""` | What to avoid |

### Models

Static list (no live API): `seedance-2.0`, `seedance-1.0-pro`, `seedance-1.0-lite`.

### Default Pricing (per request)

| Model | Price |
|-------|-------|
| `seedance-2.0` | $0.030 |

### Limitations

- Tasks stored in memory — lost on restart
- Task IDs are sequential (`seed-1`, `seed-2`, ...)

## Related

- [tool-veo](tool-veo.md) — Alternative video generator (Google Veo)
- [messaging-telegram](messaging-telegram.md) — Async video polling integration
