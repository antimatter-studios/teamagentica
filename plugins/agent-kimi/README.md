# agent-kimi

AI agent powered by Moonshot's Kimi K2 models with 128K context, thinking mode variants, and SSE streaming.

## Overview

Wraps Moonshot's Kimi API with OpenAI-compatible tool calling format. Supports tool-use loop, SSE streaming, dynamic model listing, and hot config reload. Reports usage to `cost:tracking` as provider `moonshot`.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `KIMI_API_KEY` | string (secret) | -- | Moonshot Kimi API key |
| `KIMI_MODEL` | select (dynamic) | `kimi-k2-turbo-preview` | Model to use |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check |
| POST | `/chat` | Chat completion with tool-use loop |
| POST | `/chat/stream` | SSE streaming chat completion |
| GET | `/mcp` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show system prompts |
| GET | `/models` | List models from Kimi API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (KIMI_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:**
- `config:update` -- hot-reloads model, API key, and debug flag without restart

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

1. Discovers tools from `tool:*` plugins (cached 60s), converts to OpenAI-style function definitions.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. Tool-use loop (max 20 iterations): calls Kimi API, checks for `tool_calls` finish reason, executes each tool via kernel proxy, appends results, and loops.
4. Media attachments from tool results are extracted and returned separately.
5. Reports usage to cost-tracking as provider `moonshot`.

## Models

6 model variants with different speed/capability/price tradeoffs:

- `kimi-k2-turbo-preview` -- premium, fast
- `kimi-k2.5` -- mid-tier
- `kimi-k2-0905-preview`, `kimi-k2-0711-preview` -- budget
- `kimi-k2-thinking`, `kimi-k2-thinking-turbo` -- chain-of-thought reasoning

## Notes

- Uses OpenAI-compatible tool calling format (tool_calls finish reason, tool role messages).
- Provider name in cost reports is `moonshot` (not `kimi`).
- Config is hot-reloadable via `config:update` events.
