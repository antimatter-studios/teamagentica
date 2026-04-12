# agent-anthropic

AI agent powered by Anthropic Claude models with CLI and API key backends, workspace support, MCP integration, and SSE streaming.

## Overview

Wraps Anthropic's Claude models with two backends: **CLI mode** (uses the `claude` CLI binary with OAuth authentication, workspace isolation, and MCP tool access) and **API key mode** (direct Anthropic REST API with full tool-use loop). Reports usage to `cost:tracking` as provider `anthropic`.

## Capabilities

- `agent:openai` -- OpenAI-compatible chat interface
- `agent:chat` -- chat completions
- `agent:completion` -- text completions

**Dependency:** `cost:tracking`

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `CLAUDE_BACKEND` | select | `cli` | `cli` or `api_key` |
| `CLAUDE_AUTH` | oauth | -- | Anthropic OAuth login (visible when backend=cli) |
| `ANTHROPIC_API_KEY` | string (secret) | -- | API key (visible when backend=api_key) |
| `CLAUDE_MODEL` | select (dynamic) | `claude-sonnet-4-6` | Model to use |
| `CLAUDE_SKIP_PERMISSIONS` | boolean | `false` | Auto-approve all CLI tool use without prompting (visible when backend=cli) |
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
| GET | `/config/options/:field` | Dynamic select options (CLAUDE_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| ANY | `/mcp-proxy` | Reverse proxy to infra-mcp-server via mTLS |
| GET | `/auth/status` | CLI backend auth status |
| POST | `/auth/device-code` | Start OAuth device-code flow |
| POST | `/auth/submit-code` | Submit one-time code for OAuth |
| DELETE | `/auth` | Logout / clear tokens |
| GET | `/pricing` | Get pricing table |
| PUT | `/pricing` | Update pricing table |

## Events

**Subscribes to:**
- `config:update` -- hot-reloads model, API key, and debug flag without restart
- `mcp_server:enabled` -- writes MCP config for CLI backend
- `mcp_server:disabled` -- removes MCP config

**Emits:**
- `chat_request`, `chat_response`, `error`, `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop`

## How It Works

### CLI backend (`CLAUDE_BACKEND=cli`)
1. Builds a prompt string from conversation history.
2. Calls the `claude` CLI binary with model, prompt, system prompt, max turns, and optional MCP config.
3. Supports workspace isolation via `workspace_id` and session resumption via `session_id`.
4. MCP integration: CLI subprocess connects to `/mcp-proxy` on localhost, which forwards to `infra-mcp-server` via mTLS.

### API key backend (`CLAUDE_BACKEND=api_key`)
1. Discovers tools from `tool:*` plugins (cached 60s).
2. Tool-use loop (max 20 iterations): calls Anthropic API, checks for `tool_use` stop reason, executes tools via kernel proxy, appends results, and loops.
3. Media attachments (images from tool results) extracted and returned separately.

## Notes

- Two completely different code paths for CLI vs API key backends.
- OAuth device-code flow for CLI backend -- user authenticates via browser.
- MCP integration is CLI-only; proactively discovers infra-mcp-server at startup.
- Tool names are prefixed as `pluginID__toolName` to avoid collisions.
