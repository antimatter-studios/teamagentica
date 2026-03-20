# tool-veo

AI video generation powered by Google Veo via Gemini API.

## Overview

Submits video generation requests to the Google Veo API (`predictLongRunning`) and tracks them as async tasks. Callers poll a status endpoint to get the video URI when generation completes. Supports configurable aspect ratio and negative prompts.

## Capabilities

- `agent:tool:video-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string | yes | — | Google Gemini API key |
| `VEO_MODEL` | select (dynamic) | no | `veo-3.1-generate-preview` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Submit video generation (returns task_id, 202 Accepted) |
| GET | `/status/:taskId` | Poll task status |
| GET | `/tools` | Tool schema for agent discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/config/options/:field` | Dynamic model list (filters to `predictLongRunning` models) |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |

## Events

- **Emits:** `generate_request`, `generate_submitted`, `generate_complete`, `error`
- **Reports usage** to cost-tracking

## How It Works

1. Agent sends prompt to `/generate`
2. Plugin POSTs to `generativelanguage.googleapis.com/v1beta/models/{model}:predictLongRunning`
3. API returns an operation name; plugin creates an internal task and returns 202
4. Agent polls `/status/:taskId`; plugin calls `GET /v1beta/{operationName}` to check progress
5. When `done: true`, extracts video URI from `response.generateVideoResponse.generatedSamples[0].video.uri`
6. Completed results are cached in-memory for subsequent polls

## Gotchas / Notes

- **Asynchronous** generation with polling pattern
- Default aspect ratio is `16:9` (unlike image tools which default to `1:1`)
- Tasks are in-memory only; lost on restart
- Dynamic model list filters to models that support `predictLongRunning`
- Pricing: $0.025 per request
