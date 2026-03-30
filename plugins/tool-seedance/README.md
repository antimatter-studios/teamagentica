# tool-seedance

AI video generation powered by the Seedance 2.0 API. Asynchronous -- submits generation requests and delivers results via webhook callbacks or polling.

## Capabilities

- `agent:tool:video-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `SEEDANCE_API_KEY` | string | yes | -- | Seedance API key |
| `SEEDANCE_MODEL` | select (dynamic) | no | `seedance-2.0` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Submit video generation (returns task_id, 202 Accepted) |
| GET | `/status/:taskId` | Poll task status (processing/completed/failed) |
| POST | `/chat` | Chat-format wrapper -- blocks polling or returns async with webhook |
| POST | `/callback/:taskId` | Webhook callback from Seedance API |
| GET | `/models` | List available models |
| GET | `/mcp` | Tool schema for agent/MCP discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/config/options/:field` | Dynamic select options |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |
| GET | `/schema` | Plugin schema (SDK) |
| POST | `/events` | Event handler (SDK) |

## Tools Exposed

- **generate_video** -- text prompt to video. Parameters: `prompt` (required), `aspect_ratio`, `resolution` (480p/720p), `duration` (4/8/12s), `generate_audio`, `fixed_lens`.
- **check_video_status** -- poll task completion. Parameters: `task_id` (required).

## Events

- Emits: `generate_request`, `generate_submitted`, `generate_complete`, `generate_failed`, `chat_request`, `chat_response`, `error`
- Reports usage to `cost:tracking` on submit and completion
- Reports relay progress updates for async delivery to messaging clients

## How It Fits

Agents call `/generate` or `/chat`. The plugin POSTs to `seedanceapi.org/v1/generate` and gets back a task_id. Two completion delivery paths:

1. **Webhook** (preferred): Plugin registers a webhook route via ingress. Seedance calls back on completion. Plugin reports progress to relay via event bus.
2. **Polling fallback**: Background goroutine polls Seedance status API every 30s. `/chat` without webhook polls synchronously for up to 100s.

Tasks are stored in-memory only; lost on restart. Pricing is per-request ($0.14/video).
