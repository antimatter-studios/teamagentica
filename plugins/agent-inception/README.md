# agent-inception

AI agent powered by Inception Labs Mercury 2, the world's fastest diffusion-based reasoning LLM.

## Overview

Wraps the Inception Labs API (`api.inceptionlabs.ai/v1`). Supports chat completions with tool use, plus specialized code-editing endpoints (apply-edit, next-edit, fill-in-the-middle). Unique features include diffusion-mode inference and configurable reasoning effort.

## Capabilities

- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `INCEPTION_API_KEY` | string (secret) | -- | Inception Labs API key |
| `INCEPTION_MODEL` | select (dynamic) | `mercury-2` | Model to use |
| `INCEPTION_INSTANT` | boolean | `false` | Enable instant (lowest latency) reasoning mode |
| `INCEPTION_DIFFUSING` | boolean | `false` | Enable diffusion-based inference |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check |
| POST | `/chat` | Chat completion with tool-use loop |
| GET | `/tools` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show coordinator and direct system prompts |
| GET | `/models` | List models from Inception API |
| GET | `/config/options/:field` | Dynamic select options (INCEPTION_MODEL) |
| POST | `/apply-edit` | Apply a code edit using mercury-edit model |
| POST | `/next-edit` | Predict next edit using mercury-edit model |
| POST | `/fim` | Fill-in-the-middle autocomplete using mercury-edit |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:** none

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`
- `apply_edit_request`, `apply_edit_response`, `next_edit_request`, `next_edit_response`, `fim_request`, `fim_response`

## How It Works

### Chat
1. Discovers tools from `tool:*` plugins (cached 60s), converts to OpenAI-style `ToolDef` format.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. Applies request options: diffusing mode, reasoning effort level, and tool definitions.
4. **Tool-use loop** (max 20 iterations, configurable via `TOOL_LOOP_LIMIT`): calls `inception.ChatCompletion()`, checks for `tool_calls` finish reason, executes tools via kernel proxy, appends results, and loops.
5. Per-request overrides: `reasoning_effort` ("instant", "low", "medium", "high"), `diffusing` (bool), `model`.

### Code editing endpoints
- **`/apply-edit`**: takes `original_code` + `update_snippet`, returns the full updated code via mercury-edit.
- **`/next-edit`**: takes recent snippets, current file content, and edit history; predicts the next edit.
- **`/fim`**: fill-in-the-middle completion with `prompt` and `suffix`.

## Gotchas / Notes

- **Diffusion-based LLM**: Mercury uses a diffusion architecture, not autoregressive. The `diffusing` flag enables this mode, and `reasoning_effort` controls quality/speed tradeoff.
- **Three specialized models**: `mercury-2` (chat), `mercury-coder-small` (code), `mercury-edit` (editing/FIM).
- **Code editing endpoints are unique to this plugin** -- no other agent plugin has `/apply-edit`, `/next-edit`, or `/fim`.
- Per-request `reasoning_effort` overrides the global `INCEPTION_INSTANT` setting.
- `TOOL_LOOP_LIMIT` is configurable (default 20) for controlling max tool iterations.
