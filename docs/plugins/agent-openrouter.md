# agent-openrouter

> Multi-model access via OpenRouter (GPT-4o, Claude, Gemini, Llama, and more).

## Overview

The OpenRouter agent plugin provides AI chat through OpenRouter's unified API, giving access to models from OpenAI, Anthropic, Google, Meta, and many other providers through a single API key.

## Capabilities

- `agent:chat` — General AI chat interface
- `agent:chat:openrouter` — OpenRouter-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `OPENROUTER_API_KEY` | string (secret) | yes | — | OpenRouter API key (openrouter.ai/keys) |
| `OPENROUTER_MODEL` | select (dynamic) | no | `google/gemini-2.5-flash` | Default model |
| `OPENROUTER_DATA_PATH` | string | no | `/data` | Usage data directory |
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

- OpenAI-compatible API at `https://openrouter.ai/api/v1/chat/completions`
- Custom headers: `HTTP-Referer: https://teamagentica.dev`, `X-Title: TeamAgentica`
- Dynamic model listing returns all OpenRouter models (no filtering)
- No vision or tool calling support

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `google/gemini-2.5-flash` | $0.15 | $0.60 | $0.0375 |
| `openai/gpt-4o` | $2.50 | $10.00 | $1.25 |
| `anthropic/claude-sonnet-4` | $3.00 | $15.00 | $0.30 |
| `google/gemini-2.5-pro` | $1.25 | $10.00 | $0.3125 |
| `meta-llama/llama-4-maverick` | $0.50 | $1.50 | — |

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [agent-requesty](agent-requesty.md) — Similar multi-provider router
