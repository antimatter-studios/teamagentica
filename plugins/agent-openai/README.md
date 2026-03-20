# agent-openai

AI agent powered by OpenAI GPT and Codex models with chat and completion capabilities.

## Overview

Wraps OpenAI's API. Supports two backends: **subscription mode** (uses the Codex CLI binary with device-code OAuth) and **API key mode** (direct OpenAI REST API with full tool-use loop). Reports usage to `infra-cost-tracking` via the SDK.

## Capabilities

- `agent:openai` -- OpenAI-compatible chat interface
- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `OPENAI_BACKEND` | select | `subscription` | `subscription` or `api_key` |
| `OPENAI_AUTH` | oauth | -- | OpenAI OAuth login (visible when backend=subscription) |
| `OPENAI_API_KEY` | string (secret) | -- | API key (visible when backend=api_key) |
| `OPENAI_MODEL` | select (dynamic) | `gpt-4o` | Model to use |
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check with backend and configured status |
| POST | `/chat` | Chat completion (see below) |
| GET | `/tools` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show coordinator and direct system prompts |
| GET | `/models` | List available models + current default |
| GET | `/config/options/:field` | Dynamic select options (OPENAI_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/auth/status` | Codex CLI auth status |
| POST | `/auth/device-code` | Start OAuth device-code flow |
| POST | `/auth/poll` | Poll for device-code auth completion |
| DELETE | `/auth` | Logout / clear tokens |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:**
- `mcp_server:enabled` -- writes MCP config for Codex CLI (subscription backend only)
- `mcp_server:disabled` -- removes MCP config

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

### Subscription backend (`OPENAI_BACKEND=subscription`)
1. Uses the Codex CLI binary (`/usr/local/bin/codex`) for chat completions.
2. Builds system prompt and prepends to conversation as a system message.
3. Supports **workspace isolation** via `workspace_id` (maps to `/workspaces/<id>`).
4. Calls `codexCLI.ChatCompletion()` with model, messages, image URLs, and workspace directory.
5. **Model auto-switch**: when using subscription backend with the default `gpt-4o`, model is automatically changed to `gpt-5.3-codex`.

### API key backend (`OPENAI_BACKEND=api_key`)
1. Discovers tools from `tool:*` plugins (cached 60s), converts to OpenAI `ToolDef` format.
2. Builds system prompt (coordinator or direct mode) and prepends to conversation.
3. **Tool-use loop** (max 20 iterations, configurable via `TOOL_LOOP_LIMIT`): calls OpenAI API, checks for `tool_calls` finish reason, executes each tool via kernel proxy, appends results, and loops.
4. Media attachments from tool results are extracted and returned separately.
5. Supports custom API endpoint via `OPENAI_API_ENDPOINT`.

### System prompt injection
- Coordinator mode: includes JSON task plan routing instructions with parallel execution support.
- Direct mode: identifies as `@alias` and lists available tools.

## Gotchas / Notes

- **Two completely different code paths** for subscription vs API key backends. Subscription mode delegates to the Codex CLI; API key mode implements the full tool loop in Go.
- **Model auto-switch**: subscription backend silently changes `gpt-4o` to `gpt-5.3-codex` as the default.
- **OAuth device-code flow** for subscription backend -- user authenticates via browser, then polls `/auth/poll`.
- **MCP integration** is subscription-only: writes Codex-compatible MCP config on `mcp_server:enabled/disabled` events.
- **Image support**: accepts `image_urls` in chat request, attached to the last user message.
- **Workspace support**: `workspace_id` routes the Codex CLI to work in a specific directory.
- Tool names are prefixed as `pluginID__toolName` to avoid collisions.
- `TOOL_LOOP_LIMIT` (default 20) and `CODEX_CLI_TIMEOUT` (default 300s) are tunable.
