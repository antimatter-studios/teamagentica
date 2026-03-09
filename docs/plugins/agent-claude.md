# agent-claude

> Anthropic Claude agent with tool use, MCP integration, and CLI/API key backends.

## Overview

The Claude plugin provides access to Anthropic's Claude models. It supports two backends: the Claude CLI (subscription-based) and direct API key access. Includes tool use support with automatic discovery of registered tool plugins, and MCP (Model Context Protocol) server integration for additional tool capabilities.

## Capabilities

- `ai:chat` — AI chat provider
- `ai:chat:anthropic` — Anthropic-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `CLAUDE_BACKEND` | select | no | `cli` | Backend: `cli` (Claude CLI) or `api_key` (direct API) |
| `ANTHROPIC_API_KEY` | string (secret) | yes | — | Anthropic API key (required for api_key backend) |
| `CLAUDE_MODEL` | select | no | `claude-sonnet-4-6` | Model selection (fetched dynamically) |
| `PLUGIN_ALIASES` | aliases | no | — | Alias configuration |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `POST` | `/chat` | Chat completion with optional tool use |
| `GET` | `/models` | List available models |
| `GET` | `/tools` | List discovered tools |
| `GET` | `/system-prompt` | View generated system prompts |
| `GET` | `/config/options/:field` | Dynamic model options |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Detailed usage records |
| `GET` | `/pricing` | Model pricing |
| `PUT` | `/pricing` | Update pricing |

## Events

### Subscriptions

- `mcp_server:enabled` — MCP server came online, re-discover tools
- `mcp_server:disabled` — MCP server went offline

## Chat Request

The `/chat` endpoint accepts:

```json
{
  "message": "Hello",
  "model": "claude-sonnet-4-6",
  "image_urls": ["https://..."],
  "conversation": [{"role": "user", "content": "..."}],
  "is_coordinator": true,
  "agent_alias": "claude"
}
```

When `is_coordinator` is true, the agent builds a system prompt that includes available aliases and tool descriptions for coordinator delegation.

## Related

- [agent-openai](agent-openai.md), [agent-gemini](agent-gemini.md) — Other agent plugins
- [Plugin SDK](../plugin-sdk.md) — Tool discovery and alias integration
