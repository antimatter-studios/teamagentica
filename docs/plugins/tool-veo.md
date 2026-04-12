# tool-veo

> Google Veo video generation with async polling.

## Overview

The Veo plugin generates videos using Google's Veo models via the Gemini API's long-running operations endpoint. Uses an asynchronous flow similar to tool-seedance: submit a request, then poll for completion.

## Capabilities

- `agent:tool:video` — Video generation tool
- `agent:tool:video:veo` — Veo-specific

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string (secret) | yes | — | Google AI Studio API key |
| `VEO_MODEL` | select (dynamic) | no | `veo-3.1-generate-preview` | Default model |
| `VEO_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/generate` | Submit video generation (returns `task_id`) |
| `GET` | `/status/:taskId` | Poll generation status |
| `GET` | `/config/options/:field` | Dynamic model list via Gemini API |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Raw records |
| `GET` | `/pricing` | Get pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Emissions

- `generate_request` — On submission
- `generate_submitted` — After API submission
- `generate_complete` — On status resolution (completed or failed)
- `error` — On submission failure
- `usage:report` — Addressed to infra-cost-explorer

## Usage

### Generate Video

```json
POST /generate
{
  "prompt": "A timelapse of clouds moving over a city skyline",
  "aspect_ratio": "16:9",
  "negative_prompt": "blurry, shaky"
}
```

Response (202):
```json
{
  "task_id": "veo-1",
  "status": "processing",
  "message": "Video generation started"
}
```

### Poll Status

```
GET /status/veo-1
```

Response (when complete):
```json
{
  "task_id": "veo-1",
  "status": "completed",
  "video_uri": "https://...",
  "model": "veo-3.1-generate-preview",
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
| `negative_prompt` | string | `""` | What to avoid |

### Models

Dynamic list from Gemini API (filters to models supporting `predictLongRunning`). Fallback: `veo-3.1-generate-preview`, `veo-3-generate-preview`.

### Default Pricing (per request)

| Model | Price |
|-------|-------|
| `veo-3.1-generate-preview` | $0.025 |

### Technical Details

- Uses Gemini's `predictLongRunning` API (Vertex AI predict format)
- Returns a Google Long-Running Operation with `name` field for polling
- Video URI extracted from `response.generateVideoResponse.generatedSamples[0].video.uri`
- Tasks stored in memory — lost on restart
- Shares `GEMINI_API_KEY` with agent-google and tool-nanobanana

## Related

- [tool-seedance](tool-seedance.md) — Alternative video generator (Seedance)
- [tool-nanobanana](tool-nanobanana.md) — Uses same Gemini API for images
- [messaging-telegram](messaging-telegram.md) — Async video polling integration
