# agent-openai

> OpenAI GPT-4o, o1, and Codex models via API key or Codex subscription.

## Overview

The OpenAI agent plugin provides AI chat capabilities through two backends: direct OpenAI API access (API key) and Codex CLI subscription. It supports tool calling, vision, usage tracking, and MCP integration. This is the most feature-rich agent plugin.

## Capabilities

- `ai:chat` — General AI chat interface
- `ai:chat:openai` — OpenAI-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `OPENAI_BACKEND` | select | no | `subscription` | `"subscription"` (Codex CLI) or `"api_key"` (direct API) |
| `OPENAI_API_KEY` | string (secret) | when backend=api_key | — | OpenAI API key |
| `OPENAI_MODEL` | select (dynamic) | no | `gpt-4o` / `gpt-5.3-codex` | Default model (auto-switched by backend) |
| `OPENAI_API_ENDPOINT` | string | no | `https://api.openai.com/v1` | API base URL |
| `CODEX_DATA_PATH` | string | no | `/data` | Codex CLI data directory |
| `CODEX_CLI_BINARY` | string | no | `/usr/local/bin/codex` | Codex CLI binary path |
| `CODEX_CLI_TIMEOUT` | int | no | `300` | CLI execution timeout (seconds) |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (includes backend + configured status) |
| `POST` | `/chat` | Chat completion (dual-backend) |
| `GET` | `/models` | List available models |
| `GET` | `/config/options/:field` | Dynamic select options |
| `GET` | `/usage` | Usage summary (today/week/all-time) |
| `GET` | `/usage/records` | Raw request records (`?since=RFC3339`) |
| `GET` | `/pricing` | Get pricing table |
| `PUT` | `/pricing` | Update pricing table |
| `GET` | `/auth/status` | Codex CLI auth state |
| `POST` | `/auth/device-code` | Start device-code OAuth flow |
| `POST` | `/auth/poll` | Poll login completion |
| `DELETE` | `/auth` | Logout (clear Codex tokens) |

## Events

### Subscriptions

- `mcp_server:enabled` — Writes MCP config to Codex CLI `config.toml` (subscription backend only)
- `mcp_server:disabled` — Removes MCP config from `config.toml`

### Emissions

- `chat_request` — When a chat request starts
- `chat_response` — When a response is received
- `error` — On failure
- `tool_discovery`, `tool_call`, `tool_result`, `tool_error`, `tool_loop` — Tool calling lifecycle
- `usage:report` — Addressed to infra-cost-explorer with token counts

## Features

### Dual Backend

- **Subscription**: Invokes Codex CLI binary (`codex exec --json --full-auto`) as a subprocess. Supports device-code OAuth login flow.
- **API Key**: Direct OpenAI REST API calls. Supports tool calling and vision.

### Tool Calling (API Key Backend Only)

Discovers `tool:*` plugins from the kernel, builds OpenAI function-calling definitions (`pluginID__toolName`), and runs a tool-use loop (max 5 iterations). Tool results with `image_data` are extracted as attachments.

### Vision

- **API key**: Images sent as `image_url` content parts
- **Subscription**: Images downloaded to temp files, passed via `--image` flag

### Models

Dynamic list via API (filters `gpt-*`, `o1-*`, `o3-*`, `o4-*`, `chatgpt-*`, `codex-*`). Hardcoded fallback: `gpt-4.1`, `gpt-4.1-mini`, `gpt-4.1-nano`, `gpt-4o`, `gpt-4o-mini`, `o3`, `o3-mini`, `o4-mini`.

### Default Pricing (per 1M tokens)

| Model | Input | Output | Cached |
|-------|-------|--------|--------|
| `gpt-4o` | $2.50 | $10.00 | $1.25 |
| `gpt-4o-mini` | $0.15 | $0.60 | $0.075 |
| `o4-mini` | $1.10 | $4.40 | $0.275 |

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [cost-explorer](infra-cost-explorer.md) — Usage tracking
- [mcp-server](infra-mcp-server.md) — MCP integration
