# agent-gemini

AI agent powered by Google Gemini models including Flash and Pro.

## Overview

Wraps Google's Gemini API. Single backend using API key authentication. Supports tool-use loop with function calling, image inputs, and dynamic model listing from the Gemini API.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions

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
| GET | `/tools` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show coordinator and direct system prompts |
| GET | `/models` | List models from Gemini API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (GEMINI_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:** none

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

1. Discovers tools from `tool:*` plugins (cached 60s), converts to Gemini `FunctionDeclaration` format.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. Calls Gemini API via `ChatCompletionWithTools()` (or `ChatCompletion()` if no tools).
4. **Tool-use loop** (max 20 iterations): if the response contains a `FunctionCall`, executes it via kernel proxy, appends the `FunctionResponse` to the conversation, and loops. Image URLs are only sent on the first iteration.
5. Media attachments from tool results are extracted and returned separately.
6. Reports usage to cost-tracking via SDK.

### Model listing
- When API key is set, fetches available models from the Gemini API.
- Falls back to hardcoded defaults (`gemini-2.5-flash`, etc.) if API call fails.

## Gotchas / Notes

- **Image support**: accepts `image_urls` in chat request; URLs are attached to the last user message.
- **Gemini function calling** uses a different format than OpenAI (single function call per turn, `FunctionResponse` instead of `tool` role).
- Images are cleared from subsequent iterations after the first tool call to avoid re-sending.
- No config hot-reload -- config is immutable after startup.
