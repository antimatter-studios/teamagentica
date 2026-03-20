# tool-seedance

AI video generation powered by the Seedance 2.0 API.

## Overview

Submits video generation requests to the Seedance API and tracks them as async tasks. Supports text-to-video and image-to-video with configurable aspect ratio, resolution, duration, and optional audio generation. Callers poll a status endpoint to retrieve the final video URL.

## Capabilities

- `agent:tool:video-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `SEEDANCE_API_KEY` | string | yes | — | Seedance API key |
| `SEEDANCE_MODEL` | select (dynamic) | no | `seedance-2.0` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Submit video generation (returns task_id, 202 Accepted) |
| GET | `/status/:taskId` | Poll task status (processing/completed/failed) |
| GET | `/tools` | Tool schema for agent discovery |
| GET | `/system-prompt` | System prompt for this tool |
| GET | `/config/options/:field` | Dynamic select options |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |

## Events

- **Emits:** `generate_request`, `generate_submitted`, `generate_complete`, `generate_failed`, `error`
- **Reports usage** to cost-tracking on submit and completion

## How It Works

1. Agent sends prompt to `/generate`
2. Plugin POSTs to `seedanceapi.org/v1/generate` with prompt + options
3. Seedance returns a `task_id`; plugin creates an internal task tracking entry and returns 202
4. Agent polls `/status/:taskId` -- plugin calls `seedanceapi.org/v1/status?task_id=` on each poll
5. On completion, the video URL is cached and returned on subsequent polls
6. Pricing is per-request ($0.14/video)

## Gotchas / Notes

- Generation is **asynchronous** -- `/generate` returns immediately with a task_id
- Supports image-to-video via `image_urls` (max 1 reference image)
- Tasks are stored in-memory only; lost on restart
- Status mapping: Seedance `SUCCESS` -> `completed`, `IN_PROGRESS` -> `processing`, `FAILED` -> `failed`
