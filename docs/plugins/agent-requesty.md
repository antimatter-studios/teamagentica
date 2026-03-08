# agent-requesty

> Multi-model access via Requesty router.

## Overview

The Requesty agent plugin provides AI chat through Requesty's unified routing API, offering access to models from multiple providers through a single API key. Structurally similar to the OpenRouter plugin.

## Capabilities

- `ai:chat` — General AI chat interface
- `ai:chat:requesty` — Requesty-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `REQUESTY_API_KEY` | string (secret) | yes | — | Requesty API key (app.requesty.ai) |
| `REQUESTY_MODEL` | select (dynamic) | no | `google/gemini-2.5-flash` | Default model |
| `REQUESTY_DATA_PATH` | string | no | `/data` | Usage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/chat` | Chat completion |
| `GET` | `/models` | List all available models |
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

- OpenAI-compatible API at `https://router.requesty.ai/v1/chat/completions`
- Dynamic model listing returns all Requesty models
- No vision or tool calling support

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `google/gemini-2.5-flash` | $0.15 | $0.60 | $0.0375 |
| `openai/gpt-4o` | $2.50 | $10.00 | $1.25 |
| `anthropic/claude-sonnet-4-20250514` | $3.00 | $15.00 | $0.30 |
| `google/gemini-2.5-pro` | $1.25 | $10.00 | $0.3125 |
| `openai/gpt-4o-mini` | $0.15 | $0.60 | $0.075 |

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [agent-openrouter](agent-openrouter.md) — Similar multi-provider router
