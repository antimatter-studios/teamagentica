# agent-openrouter

AI router providing access to hundreds of models from OpenAI, Anthropic, Google, Meta and more via OpenRouter.

## Overview

Wraps the OpenRouter API. Simple passthrough agent -- sends chat requests to OpenRouter and returns the response. No tool-use loop, no system prompt injection, no coordinator support. Ideal for accessing many providers through a single API key.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `OPENROUTER_API_KEY` | string (secret) | -- | OpenRouter API key |
| `OPENROUTER_MODEL` | select (dynamic) | `google/gemini-2.5-flash` | Model to use (provider/model format) |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check |
| POST | `/chat` | Chat completion (single call, no tool loop) |
| GET | `/models` | List models from OpenRouter API (falls back to defaults) |
| GET | `/config/options/:field` | Dynamic select options (OPENROUTER_MODEL) |
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
2. Makes a single call to `openrouter.ChatCompletion()` with the selected model.
3. Returns the response with token usage.
4. Reports usage to cost-tracking as provider `openrouter`.

### Model listing
- Fetches available models from the OpenRouter API when API key is set.
- Falls back to hardcoded defaults if API call fails.

## Gotchas / Notes

- **No tool-use loop** -- this is a simple passthrough. Unlike agent-claude, agent-gemini, agent-openai, agent-inception, and agent-kimi, it does not discover or execute tools.
- **No system prompt injection** -- does not build coordinator/direct system prompts.
- **No `/tools`, `/system-prompt` endpoints** -- not registered.
- Model names use `provider/model` format (e.g. `google/gemini-2.5-flash`, `anthropic/claude-sonnet-4`).
- Pricing is tracked per-model through OpenRouter, but actual costs depend on the upstream provider.
