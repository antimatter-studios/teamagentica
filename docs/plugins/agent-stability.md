# tool-stability

> Stability AI image generation (Stable Diffusion 3).

## Overview

The Stability plugin generates images using Stability AI's Stable Diffusion 3 API. Provides synchronous text-to-image generation with configurable aspect ratios, negative prompts, and output formats.

## Capabilities

- `agent:tool:image` — Image generation tool
- `agent:tool:image:stability` — Stability AI-specific

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `STABILITY_API_KEY` | string (secret) | yes | — | Stability AI API key |
| `STABILITY_MODEL` | select (dynamic) | no | `sd3-medium` | Default model |
| `STABILITY_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/generate` | Generate image (synchronous) |
| `GET` | `/config/options/:field` | Dynamic model options |
| `GET` | `/usage` | Usage summary (today + all-time + per-model) |
| `GET` | `/usage/records` | Raw records (`?since=RFC3339`) |
| `GET` | `/pricing` | Get pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Emissions

- `generate_request` — On generation start
- `generate_complete` — On success (includes duration + seed)
- `error` — On failure
- `usage:report` — Addressed to infra-cost-explorer

## Usage

### Generate Image

```json
POST /generate
{
  "prompt": "A cyberpunk cityscape at sunset",
  "negative_prompt": "blurry, low quality",
  "aspect_ratio": "16:9",
  "output_format": "png"
}
```

Response:
```json
{
  "status": "success",
  "image_data": "<base64>",
  "mime_type": "image/png",
  "seed": 12345,
  "model": "sd3-medium",
  "prompt": "A cyberpunk cityscape at sunset"
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `prompt` | string | required | Image description |
| `negative_prompt` | string | `""` | What to avoid (ignored on `sd3-large-turbo`) |
| `aspect_ratio` | string | `1:1` | `1:1`, `16:9`, `9:16`, etc. |
| `output_format` | string | `png` | `png` or `jpeg` |

### Models

Dynamic list from Stability engines API. Fallback: `sd3-medium`, `sd3-large`, `sd3-large-turbo`.

### Default Pricing (per request)

| Model | Price |
|-------|-------|
| `sd3-medium` | $0.035 |
| `sd3-large` | $0.065 |
| `sd3-large-turbo` | $0.040 |

## Related

- [tool-nanobanana](tool-nanobanana.md) — Alternative image generator (Gemini)
- [agent-openai](agent-openai.md) — Can call this tool via function calling
