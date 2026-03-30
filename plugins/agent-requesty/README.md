# agent-requesty

AI router providing unified access to OpenAI, Anthropic, Google and more via Requesty.

## Overview

Wraps the Requesty API. Simple passthrough agent -- sends chat requests to Requesty and returns the response. No tool-use loop, no system prompt injection. Structurally identical to agent-openrouter, targeting the Requesty router instead.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `REQUESTY_API_KEY` | string (secret) | -- | Requesty API key |
| `REQUESTY_MODEL` | select (dynamic) | `google/gemini-2.5-flash` | Model to use (provider/model format) |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check |
| POST | `/chat` | Chat completion (single call, no tool loop) |
| GET | `/models` | List models from Requesty API |
| GET | `/config/options/:field` | Dynamic select options (REQUESTY_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:**
- `config:update` -- hot-reloads API key, model, and debug settings

**Emits:**
- `chat_request`, `chat_response`, `error`

## How It Works

1. Accepts a chat request with message or conversation history.
2. Makes a single call to `requesty.ChatCompletion()` with the selected model.
3. Returns the response with token usage.
4. Reports usage to cost-tracking as provider `requesty`.

Per-request model override is supported via the `model` field in the request body.

## Notes

- No tool-use loop -- simple passthrough. Does not discover or execute tools.
- No system prompt injection -- does not build coordinator/direct system prompts.
- No `/mcp`, `/system-prompt` endpoints.
- Model names use `provider/model` format (e.g. `google/gemini-2.5-flash`, `anthropic/claude-sonnet-4-20250514`).
