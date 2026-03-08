# agent-kimi

> Moonshot Kimi models via OpenAI-compatible API.

## Overview

The Kimi agent plugin provides AI chat using Moonshot's Kimi models through their OpenAI-compatible API endpoint. Straightforward text-only chat without vision or tool calling.

## Capabilities

- `ai:chat` — General AI chat interface
- `ai:chat:kimi` — Kimi-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `KIMI_API_KEY` | string (secret) | yes | — | Moonshot API key (platform.moonshot.ai) |
| `KIMI_MODEL` | select (dynamic) | no | `kimi-k2-turbo-preview` | Default model |
| `KIMI_DATA_PATH` | string | no | `/data` | Usage data directory |
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
- `usage:report` — Addressed to infra-cost-explorer (provider: `moonshot`)

## Features

### Models

Dynamic list from Moonshot API (filters to IDs containing `kimi` or `moonshot`). Fallback: `kimi-k2-turbo-preview`, `kimi-k2.5`, `kimi-k2-0905-preview`, `kimi-k2-0711-preview`, `kimi-k2-thinking-turbo`, `kimi-k2-thinking`, `moonshot-v1-128k`, `moonshot-v1-32k`, `moonshot-v1-8k`, `moonshot-v1-auto`.

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `kimi-k2-turbo-preview` | $1.15 | $8.00 | $0.29 |
| `kimi-k2.5` | $0.60 | $3.00 | $0.15 |
| `kimi-k2-thinking-turbo` | $1.15 | $8.00 | $0.29 |
| `kimi-k2-thinking` | $0.60 | $2.50 | $0.15 |

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [agent-openrouter](agent-openrouter.md) — Multi-provider alternative
