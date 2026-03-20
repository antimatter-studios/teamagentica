# agent-claude

AI agent powered by Anthropic Claude models with CLI and API backends, workspace support, and MCP integration.

## Overview

Wraps Anthropic's Claude models. Supports two backends: **CLI mode** (uses the `claude` CLI binary with OAuth authentication) and **API key mode** (direct Anthropic REST API calls with full tool-use loop). Reports usage to `infra-cost-tracking` via the SDK.

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
| `PLUGIN_DEBUG` | boolean | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Health check with backend and configured status |
| POST | `/chat` | Chat completion (see below) |
| GET | `/tools` | List discovered tools from tool:* plugins |
| GET | `/system-prompt` | Show coordinator and direct system prompts |
| GET | `/models` | List available models + current default |
| GET | `/config/options/:field` | Dynamic select options (e.g. CLAUDE_MODEL) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
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
1. Builds a single prompt string from the conversation history (system + user/assistant turns concatenated).
2. Calls the `claude` CLI binary via `claudecli.ChatCompletion()` with model, prompt, system prompt, max turns, and optional MCP config.
3. Supports **workspace isolation** via `workspace_id` (maps to `/workspaces/<id>`) and **session resumption** via `session_id`.
4. Returns response text, token counts, cost, number of turns, and session ID.

### API key backend (`CLAUDE_BACKEND=api_key`)
1. Discovers tools from all `tool:*` plugins via kernel's plugin search + `/tools` endpoint (cached 60s).
2. Builds system prompt with coordinator routing instructions (if `is_coordinator=true`) or agent identity.
3. Enters a **tool-use loop** (max 20 iterations): calls `anthropic.ChatCompletion()`, checks for `tool_use` stop reason, executes tools via kernel proxy (`sdk.RouteToPlugin`), appends results, and loops.
4. Media attachments (images from tool results) are extracted and returned separately; base64 data is replaced with `[image generated]` before sending back to the model.
5. Usage is reported both locally (in-memory tracker) and to the kernel via `sdk.ReportUsage()`.

### System prompt injection
- Coordinator mode: includes routing instructions for JSON task plans with parallel execution and dependency chains.
- Direct mode: identifies as `@alias` and lists available tools.
- Tool descriptions are injected into the system prompt alongside function-calling definitions.

## Gotchas / Notes

- **Two completely different code paths** for CLI vs API key backends. CLI mode delegates tool use to the CLI binary itself; API key mode implements the full tool loop in Go.
- **OAuth device-code flow** for CLI backend -- user authenticates via browser, plugin polls for completion.
- **MCP integration** is CLI-only: listens for `mcp_server:enabled/disabled` events and writes config files to `$CLAUDE_HOME/.claude.json`.
- **Hot config reload**: model, API key, and debug can change at runtime via `config:update` events without container restart.
- **Session resumption**: CLI backend supports `session_id` to continue a previous conversation.
- Tool names are prefixed as `pluginID__toolName` to avoid collisions across plugins.
