# agent-openai

AI agent powered by OpenAI GPT and Codex models with subscription and API key backends, workspace support, and MCP integration.

## Overview

Wraps OpenAI's API with two backends: **subscription mode** (uses the Codex CLI binary with device-code OAuth and workspace isolation) and **API key mode** (direct OpenAI REST API with full tool-use loop). Supports SSE streaming and hot config reload. Reports usage to `cost:tracking` as provider `openai`.

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
| POST | `/chat` | Chat completion with tool-use loop |
| POST | `/chat/stream` | SSE streaming chat completion |
| GET | `/mcp` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show system prompts |
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
- `config:update` -- hot-reloads model, API key, backend, and debug flag without restart
- `mcp_server:enabled` -- starts MCP bridge proxy and writes Codex CLI config (subscription backend only)
- `mcp_server:disabled` -- removes MCP config

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

### Subscription backend (`OPENAI_BACKEND=subscription`)
1. Uses the Codex CLI binary (`/usr/local/bin/codex`) for chat completions.
2. Supports workspace isolation via `workspace_id` (maps to `/workspaces/<id>`).
3. Model auto-switch: when using subscription backend with default `gpt-4o`, model is automatically changed to `gpt-5.3-codex`.
4. MCP integration: SDK starts an MCP bridge (localhost plain HTTP proxy forwarding to infra-mcp-server via mTLS) so Codex CLI can access MCP tools without client certs.

### API key backend (`OPENAI_BACKEND=api_key`)
1. Discovers tools from `tool:*` plugins (cached 60s), converts to OpenAI function definitions.
2. Tool-use loop (max 20 iterations, configurable via `TOOL_LOOP_LIMIT`): calls OpenAI API, checks for `tool_calls` finish reason, executes tools via kernel proxy, appends results, and loops.
3. Media attachments from tool results extracted and returned separately.
4. Supports custom API endpoint via `OPENAI_API_ENDPOINT`.

## Models

- `gpt-4o` -- default for API key mode
- `gpt-4o-mini` -- budget
- `o4-mini` -- reasoning model
- `gpt-5.1-codex` -- Codex-optimized

## Notes

- Two completely different code paths for subscription vs API key backends.
- Subscription backend silently changes `gpt-4o` to `gpt-5.3-codex` as the default model.
- OAuth device-code flow for subscription backend -- user authenticates via browser, then polls `/auth/poll`.
- MCP integration is subscription-only; proactively discovers infra-mcp-server at startup.
- Tool names are prefixed as `pluginID__toolName` to avoid collisions.
- `TOOL_LOOP_LIMIT` (default 20) and `CODEX_CLI_TIMEOUT` (default 300s) are tunable.
