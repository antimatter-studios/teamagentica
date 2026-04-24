# agent-nanobanana

AI image generation using Google Gemini's native image output. Synchronous -- returns base64-encoded image data directly in the response.

## Capabilities

- `agent:tool:image-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string | yes | -- | Google Gemini API key |
| `NANOBANANA_MODEL` | select (dynamic) | no | `gemini-2.5-flash-image` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Generate image from prompt (returns base64) |
| POST | `/chat` | Chat-format wrapper (returns attachments) |
| GET | `/models` | List available Gemini image models |
| GET | `/mcp` | Tool schema for agent/MCP discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/config/options/:field` | Dynamic select options (lists Gemini image models) |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing entries |
| PUT | `/pricing` | Update pricing entries |
| GET | `/schema` | Plugin schema (SDK) |
| POST | `/events` | Event handler (SDK) |

## Tools Exposed

- **generate_image** -- text prompt to image. Parameters: `prompt` (required), `aspect_ratio` (1:1, 16:9, 9:16).

## Events

- Emits: `generate_request`, `generate_complete`, `chat_request`, `chat_response`, `error`
- Reports usage to `cost:tracking` on every generate/chat call

## How It Fits

Agents call `/generate` or `/chat` with a text prompt. The plugin calls Gemini `generateContent` with `responseModalities: ["TEXT", "IMAGE"]` and returns the base64 image synchronously. `/chat` wraps the same flow but formats output with `attachments[]` for messaging integration. Registers tools with MCP server when available.

## Pricing

Token-based (per 1M input/output tokens), not per-request. See `plugin.yaml` for default rates per model.
