# tool-stability

AI image generation powered by Stable Diffusion 3.5 via the Stability AI API. Synchronous -- returns base64-encoded image data directly.

## Capabilities

- `agent:tool:image-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `STABILITY_API_KEY` | string | yes | -- | Stability AI API key ([get one](https://platform.stability.ai/account/keys)) |
| `STABILITY_MODEL` | select | no | `sd3.5-large` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

Available models: SD3.5 Large, SD3.5 Large Turbo, SD3.5 Medium, SD3.5 Flash.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Generate image from prompt (returns base64) |
| POST | `/chat` | Chat-format wrapper (returns attachments) |
| GET | `/models` | List available models and current selection |
| GET | `/mcp` | Tool schema for agent/MCP discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |
| GET | `/schema` | Plugin schema (SDK) |
| POST | `/events` | Event handler (SDK) |

## Tools Exposed

- **generate_image** -- text prompt to image. Parameters: `prompt` (required), `negative_prompt` (not supported on turbo/flash), `aspect_ratio`, `output_format` (png/jpeg/webp), `style_preset` (17 presets including anime, cinematic, photographic, pixel-art, etc.), `cfg_scale` (1-10), `seed`.

## Events

- Emits: `generate_request`, `generate_complete`, `chat_request`, `chat_response`, `error`
- Reports usage to `cost:tracking` on every request

## How It Fits

Agents call `/generate` or `/chat` with a text prompt. The plugin builds a multipart form POST to `api.stability.ai/v2beta/stable-image/generate/sd3` and returns the base64 image synchronously. `/chat` wraps the same flow with `attachments[]` output. Registers tools with MCP server when available.

## Pricing

Per-request: $0.065 (large), $0.04 (large-turbo), $0.035 (medium), $0.025 (flash).

## OpenAPI

Downloaded Stability AI spec in `docs/openapi.json` (as of 2026-03-23).
