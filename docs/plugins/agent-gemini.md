# agent-gemini

> Google Gemini models with vision support.

## Overview

The Gemini agent plugin provides AI chat using Google's Generative Language API. It supports vision (inline image data) and cached token tracking. No tool calling support.

## Capabilities

- `ai:chat` — General AI chat interface
- `ai:chat:gemini` — Gemini-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string (secret) | yes | — | Google AI Studio API key |
| `GEMINI_MODEL` | select (dynamic) | no | `gemini-2.5-flash` | Default model |
| `GEMINI_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/chat` | Chat completion |
| `GET` | `/models` | List available models |
| `GET` | `/config/options/:field` | Dynamic model options |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Raw records (`?since=RFC3339`) |
| `GET` | `/pricing` | Get pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Emissions

- `chat_request`, `chat_response`, `error` — Chat lifecycle events
- `usage:report` — Addressed to infra-cost-explorer

## Features

### Vision

Images are downloaded and sent as `inlineData` base64 parts. Supports Telegram's `application/octet-stream` by falling back to `image/jpeg`.

### Models

Dynamic list from Gemini API (filters to `gemini-*` models supporting `generateContent`). Fallback: `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-2.0-flash`, `gemini-2.0-flash-lite`.

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `gemini-2.5-flash` | $0.15 | $0.60 | $0.0375 |
| `gemini-2.5-pro` | $1.25 | $10.00 | $0.3125 |
| `gemini-2.0-flash` | $0.10 | $0.40 | $0.025 |

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [agent-openai](agent-openai.md) — OpenAI agent (has tool calling)
- [tool-nanobanana](tool-nanobanana.md) — Uses same Gemini API for image generation
