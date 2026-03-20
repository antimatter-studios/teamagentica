# agent-requesty

AI router providing unified access to OpenAI, Anthropic, Google and more via Requesty.

## Overview

Wraps the Requesty API. Simple passthrough agent -- sends chat requests to Requesty and returns the response. No tool-use loop, no system prompt injection, no coordinator support. Provides access to multiple AI providers through a single API key.

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
| GET | `/models` | List models from Requesty API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (REQUESTY_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:** none

**Emits:**
- `chat_request`, `chat_response`, `error`

## How It Works

1. Accepts a chat request with message or conversation history.
2. Makes a single call to `requesty.ChatCompletion()` with the selected model.
3. Returns the response with token usage.
4. Reports usage to cost-tracking as provider `requesty`.

### Model listing
- Fetches available models from the Requesty API when API key is set.
- Falls back to hardcoded defaults if API call fails.

## Gotchas / Notes

- **No tool-use loop** -- this is a simple passthrough. Unlike agent-claude, agent-gemini, agent-openai, agent-inception, and agent-kimi, it does not discover or execute tools.
- **No system prompt injection** -- does not build coordinator/direct system prompts.
- **No `/tools`, `/system-prompt` endpoints** -- not registered.
- Model names use `provider/model` format (e.g. `google/gemini-2.5-flash`, `anthropic/claude-sonnet-4-20250514`).
- **Structurally identical to agent-openrouter** -- same simple passthrough architecture, just targeting the Requesty router instead.
