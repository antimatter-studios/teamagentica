# tool-stability

AI image generation powered by Stable Diffusion 3 via the Stability AI API.

## Overview

Calls the Stability AI REST API to generate images from text prompts. Uses multipart/form-data with `Accept: application/json` to get base64-encoded image responses. Supports negative prompts, multiple aspect ratios, and output format selection.

## OpenAPI

In the `docs/openapi.json` file is the downloaded version of the specification as of 2026-03-23

## Capabilities

- `agent:tool:image-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `STABILITY_API_KEY` | string | yes | — | Stability AI API key |
| `STABILITY_MODEL` | select (dynamic) | no | `sd3-medium` | Model to use |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/generate` | Generate image from prompt (synchronous, returns base64) |
| GET | `/tools` | Tool schema for agent discovery |
| GET | `/system-prompt` | System prompt |
| GET | `/config/options/:field` | Dynamic model list from Stability engines API |
| GET | `/usage` | Usage summary |
| GET | `/usage/records` | Raw request records |
| GET | `/pricing` | Get pricing |
| PUT | `/pricing` | Update pricing |

## Events

- **Emits:** `generate_request`, `generate_complete`, `error`
- **Reports usage** to cost-tracking

## How It Works

1. Agent sends prompt + optional negative_prompt, aspect_ratio, output_format
2. Plugin builds a multipart form and POSTs to `api.stability.ai/v2beta/stable-image/generate/sd3`
3. Response contains base64-encoded image + seed value
4. Plugin validates the base64, determines MIME type from output_format, returns everything

## Gotchas / Notes

- **Synchronous** generation (like nanobanana, unlike video tools)
- `sd3-large-turbo` does not support negative prompts -- the client silently skips it
- Dynamic model list falls back to hardcoded defaults (`sd3-medium`, `sd3-large`, `sd3-large-turbo`) if the engines API fails
- Pricing is per-request: $0.035 (medium), $0.065 (large), $0.04 (large-turbo)
- 60-second HTTP timeout
