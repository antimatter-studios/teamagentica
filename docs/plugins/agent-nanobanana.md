# tool-nanobanana

> Image generation using Gemini's native image output capabilities.

## Overview

The NanoBanana plugin generates images using Google Gemini models with `responseModalities: ["TEXT", "IMAGE"]`. It returns both text and image content, making it suitable for conversational image generation. Unique among tool plugins: it has both `/generate` and `/chat` endpoints, plus a `/tools` schema endpoint.

## Capabilities

- `agent:tool:image` — Image generation tool
- `agent:tool:image:nanobanana` — NanoBanana-specific

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string (secret) | yes | — | Google AI Studio API key |
| `NANOBANANA_MODEL` | select (dynamic) | no | `gemini-2.5-flash-image` | Default model |
| `NANOBANANA_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/generate` | Generate image (synchronous) |
| `POST` | `/chat` | Chat-format wrapper (returns text + image) |
| `GET` | `/tools` | MCP-compatible tool schema |
| `GET` | `/config/options/:field` | Dynamic model list via Gemini API |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Raw records |
| `GET` | `/pricing` | Get pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Emissions

- `generate_request`, `generate_complete` — Generation lifecycle
- `chat_request`, `chat_response` — Chat lifecycle
- `error` — On failure
- `usage:report` — Addressed to infra-cost-explorer

## Usage

### Generate Image

```json
POST /generate
{
  "prompt": "A watercolor painting of a sunset over the ocean",
  "aspect_ratio": "16:9"
}
```

Response:
```json
{
  "status": "success",
  "image_data": "<base64>",
  "mime_type": "image/png",
  "text": "Here's a watercolor sunset painting...",
  "model": "gemini-2.5-flash-image",
  "prompt": "..."
}
```

### Chat Format

```json
POST /chat
{
  "message": "Draw me a cat",
  "conversation": [
    {"role": "user", "content": "I want a cute cartoon style"},
    {"role": "assistant", "content": "Sure! What animal?"}
  ]
}
```

Response:
```json
{
  "response": "Here's a cute cartoon cat!",
  "model": "gemini-2.5-flash-image",
  "attachments": [{"mime_type": "image/png", "image_data": "<base64>"}]
}
```

### Tool Schema

`GET /tools` returns:
```json
[{
  "name": "generate_image",
  "description": "Generate an image from a text prompt using Nano Banana (Gemini image model)",
  "endpoint": "/generate",
  "parameters": {
    "prompt": {"type": "string", "required": true},
    "aspect_ratio": {"type": "string", "required": false}
  }
}]
```

### Models

Dynamic list from Gemini API (filters to models containing `"image"` that support `generateContent`).

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `gemini-2.5-flash-image` | $0.15 | $0.60 | $0.0375 |
| `gemini-3.1-flash-image-preview` | $0.15 | $0.60 | $0.0375 |
| `gemini-3-pro-image-preview` | $1.25 | $10.00 | $0.3125 |

### Features

- Per-request model override via `model` parameter
- Returns both text description and generated image
- Shares `GEMINI_API_KEY` with agent-google and tool-veo

## Related

- [tool-stability](tool-stability.md) — Alternative image generator (Stability AI)
- [agent-google](agent-google.md) — Uses same Gemini API for chat
- [mcp-server](infra-mcp-server.md) — Discovers this plugin's `/tools` endpoint
