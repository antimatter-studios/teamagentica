# agent-gemini

AI agent powered by Google Gemini models with tool-use loop, SSE streaming, OpenAI-compatible proxy, and memory extraction.

## Overview

Wraps Google's Gemini API with function calling, image inputs, dynamic model listing, and hot config reload. Also exposes an OpenAI-compatible reverse proxy at `/v1/*` so other plugins can use Gemini models without their own API key. Reports usage to `cost:tracking` as provider `gemini`.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions
- `memory:extraction` -- memory extraction support

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `GEMINI_API_KEY` | string (secret) | -- | Google Gemini API key |
| `GEMINI_MODEL` | select (dynamic) | `gemini-2.5-flash` | Model to use |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check |
| POST | `/chat` | Chat completion with tool-use loop |
| POST | `/chat/stream` | SSE streaming chat completion |
| GET | `/mcp` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show system prompts |
| GET | `/models` | List models from Gemini API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (GEMINI_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |
| ANY | `/v1/*path` | OpenAI-compatible reverse proxy to Google's Gemini API |

## Events

**Subscribes to:**
- `config:update` -- hot-reloads model, API key, and debug flag without restart

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

1. Discovers tools from `tool:*` plugins (cached 60s), converts to Gemini `FunctionDeclaration` format.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. Tool-use loop (max 20 iterations): if response contains a `FunctionCall`, executes via kernel proxy, appends `FunctionResponse`, and loops. Image URLs only sent on the first iteration.
4. Media attachments from tool results extracted and returned separately.

### OpenAI-compatible proxy

The `/v1/*` routes reverse-proxy requests to Google's OpenAI-compatible endpoint (`generativelanguage.googleapis.com/v1beta/openai`), injecting the plugin's API key and stripping unsupported fields. This allows other plugins (e.g. infra-agent-memory-gateway) to use Gemini models without their own key.

## Models

- `gemini-2.5-flash` -- fast, cheap
- `gemini-2.5-pro` -- higher capability
- `gemini-2.0-flash` -- budget option

## Notes

- Gemini function calling uses a different format than OpenAI (single function call per turn, `FunctionResponse` instead of `tool` role).
- Image support: accepts `image_urls` in chat request, attached to the last user message.
- Images are cleared from subsequent tool-loop iterations after the first call.
