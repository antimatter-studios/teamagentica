# agent-kimi

AI agent powered by Moonshot's Kimi K2 with 128K context and thinking mode.

## Overview

Wraps Moonshot's Kimi API. Provides chat completions with tool-use loop support. Features dynamic model listing and multiple model variants including thinking/reasoning models.

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
| GET | `/tools` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show coordinator and direct system prompts |
| GET | `/models` | List models from Kimi API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (KIMI_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:** none

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

1. Discovers tools from `tool:*` plugins (cached 60s), converts to OpenAI-style `ToolDef` format.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. **Tool-use loop** (max 20 iterations): calls `kimi.ChatCompletion()`, checks for `tool_calls` finish reason, executes each tool via kernel proxy, appends results as `tool` role messages with `tool_call_id`, and loops.
4. Media attachments from tool results are extracted and returned separately.
5. Reports usage to cost-tracking as provider `moonshot`.

## Gotchas / Notes

- **Thinking models available**: `kimi-k2-thinking` and `kimi-k2-thinking-turbo` variants provide chain-of-thought reasoning.
- **Many model variants**: 6 models with different speed/capability tradeoffs, from k2-turbo-preview (premium) to k2-0711-preview (budget).
- Uses OpenAI-compatible tool calling format (tool_calls finish reason, tool role messages).
- No config hot-reload -- config is immutable after startup.
- Provider name in cost reports is `moonshot` (not `kimi`).
