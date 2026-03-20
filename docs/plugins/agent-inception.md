# agent-inception

> Inception Labs Mercury agent with tool use, code editing, and diffusion-based generation.

## Overview

The Inception plugin provides access to Inception Labs' Mercury models. It supports tool use with automatic discovery of registered tool plugins, coordinator delegation for multi-agent routing, and specialized code editing endpoints (apply-edit, next-edit, fill-in-the-middle). Includes optional "instant" mode for low-latency responses and "diffusing" mode to visualize the diffusion denoising process during streaming.

## Capabilities

- `agent:chat` — AI chat provider
- `agent:chat:inception` — Inception-specific provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `INCEPTION_API_KEY` | string (secret) | yes | — | Inception Labs API key |
| `INCEPTION_MODEL` | select | no | `mercury-2` | Model selection (fetched dynamically) |
| `INCEPTION_INSTANT` | boolean | no | `false` | Use reasoning_effort=instant for lowest latency (reduced quality) |
| `INCEPTION_DIFFUSING` | boolean | no | `false` | Visualize diffusion denoising in streaming responses |
| `TOOL_LOOP_LIMIT` | string | no | `20` | Maximum tool-calling iterations per request. 0 = unrestricted. |
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
| `POST` | `/apply-edit` | Apply a code edit using mercury-edit model |
| `POST` | `/next-edit` | Sequential code editing |
| `POST` | `/fim` | Fill-in-the-middle completion |
| `GET` | `/usage` | Usage summary |
| `GET` | `/usage/records` | Detailed usage records |
| `GET` | `/pricing` | Model pricing |
| `PUT` | `/pricing` | Update pricing |

## Chat Request

The `/chat` endpoint accepts:

```json
{
  "message": "Hello",
  "model": "mercury-2",
  "image_urls": ["https://..."],
  "conversation": [{"role": "user", "content": "..."}],
  "is_coordinator": true,
  "agent_alias": "mercury",
  "reasoning_effort": "instant",
  "diffusing": true
}
```

When `is_coordinator` is true, the agent builds a system prompt that includes available aliases and tool descriptions for coordinator delegation.

## Pricing

| Model | Input (per 1M tokens) | Output (per 1M tokens) |
|-------|----------------------|----------------------|
| mercury-2 | $0.25 | $0.75 |
| mercury-coder-small | $0.25 | $0.75 |
| mercury-edit | $0.25 | $0.75 |

## Related

- [agent-claude](agent-claude.md), [agent-openai](agent-openai.md), [agent-gemini](agent-gemini.md) — Other agent plugins
- [Plugin SDK](../plugin-sdk.md) — Tool discovery and alias integration
