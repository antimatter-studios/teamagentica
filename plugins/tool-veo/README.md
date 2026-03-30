# tool-veo

AI video generation powered by Google Veo via the Gemini API. Asynchronous -- submits generation requests and callers poll for completion.

## Capabilities

- `agent:tool:video-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string | yes | -- | Google Gemini API key |
| `VEO_MODEL` | select (dynamic) | no | `veo-3.1-generate-preview` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Submit video generation (returns task_id, 202 Accepted) |
| GET | `/status/:taskId` | Poll task status (processing/completed/failed) |
| GET | `/mcp` | Tool schema for agent/MCP discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/config/options/:field` | Dynamic model list (filters to `predictLongRunning` models) |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |
| GET | `/schema` | Plugin schema (SDK) |
| POST | `/events` | Event handler (SDK) |

## Tools Exposed

- **generate_video** -- text prompt to video. Parameters: `prompt` (required), `aspect_ratio`, `negative_prompt`.

## Events

- Emits: `generate_request`, `generate_submitted`, `generate_complete`, `error`
- Reports usage to `cost:tracking` on submit and completion

## How It Fits

Agents call `/generate` with a text prompt. The plugin POSTs to `generativelanguage.googleapis.com/v1beta/models/{model}:predictLongRunning`. The API returns an operation name; the plugin creates an internal task and returns 202. Callers poll `/status/:taskId`, which checks `GET /v1beta/{operationName}`. When `done: true`, the video URI is extracted and cached. Registers tools with MCP server when available.

Tasks are in-memory only; lost on restart. Dynamic model list falls back to `veo-3.1-generate-preview` and `veo-3-generate-preview` if API unavailable. Pricing is $0.025 per request.
