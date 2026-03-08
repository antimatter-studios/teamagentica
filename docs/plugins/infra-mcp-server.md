# infra-mcp-server

> Model Context Protocol server exposing TeamAgentica tools and agents to MCP clients.

## Overview

The infra-mcp-server plugin implements the Model Context Protocol (MCP) over Streamable HTTP transport. It discovers tool plugins and agent plugins from the kernel and exposes them as MCP tools, allowing external MCP clients (like Codex CLI) to interact with TeamAgentica's capabilities.

## Capabilities

- `mcp:server` — MCP protocol server

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `MCP_SERVER_PORT` | int | no | `8081` | HTTP port |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/info` | Protocol info, transport, endpoint, discovered tools |
| `POST` | `/mcp` | Streamable HTTP MCP transport (JSON-RPC 2.0) |

## Events

### Subscriptions

- `kernel:alias:update` — Invalidates tool cache and hot-swaps aliases (debounced 2s)

### Emissions

- `mcp_server:enabled` — Broadcast on startup with `{endpoint}`
- `mcp_server:disabled` — Broadcast on shutdown

## MCP Protocol

Protocol version: `2025-03-26`

### Methods

| Method | Description |
|--------|-------------|
| `initialize` | Handshake with capabilities (`tools.listChanged: true`) |
| `notifications/initialized` | Client notification (no response) |
| `tools/list` | List all available tools |
| `tools/call` | Execute a tool |
| `ping` | Health check |

### Built-in Tools

| Tool | Description |
|------|-------------|
| `list_agents` | List all `ai:chat` plugins |
| `list_tools` | List all discovered tool plugins |
| `send_message` | Send a chat message to any agent (`{agent_id, message, model?}`) |

### Discovered Tools

The MCP server discovers `tool:*` plugins from the kernel and fetches their tool definitions (via `GET /tools`). Tools are exposed with alias-prefixed names:

- `{alias}__{tool_name}` — e.g., `nb2__generate_image` (when alias exists)
- `{plugin_id}__{tool_name}` — fallback when no alias

Agent aliases also get `{alias}__chat` tools for coordinator delegation.

### Tool Cache

60-second TTL, invalidated on alias update events.

### Media Handling

Tool results with `image_data` (base64) are stored to sss3 and returned as `{{media:key}}` markers. Video URLs are returned as `{{media_url:url}}` markers. Raw base64 is never passed into LLM context.

## Related

- [agent-openai](agent-openai.md) — Subscribes to `mcp_server:enabled/disabled`
- [storage-sss3](storage-sss3.md) — Media storage
- [Plugin SDK](../plugin-sdk.md) — SDK reference
