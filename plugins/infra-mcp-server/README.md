# infra-mcp-server

Model Context Protocol (MCP) server that exposes platform tools and agent routing to all AI agents.

## Overview

Implements the MCP Streamable HTTP transport, providing a single `/mcp` endpoint that speaks JSON-RPC 2.0. Discovers all `tool:*` plugins in the platform, maps their tools through aliases, and presents them as MCP tools. Also provides builtin meta-tools for listing agents, listing tools, and sending messages to agents. This is the bridge that lets MCP-capable AI clients (Claude Desktop, Cursor, etc.) interact with the entire platform.

## Capabilities

- `infra:mcp-server`

## Dependencies

None declared, but discovers and routes to all `tool:*` and `agent:chat` plugins at runtime.

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed MCP request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/info` | Server info: protocol, transport, discovered tool count and names |
| GET | `/tools` | Raw tool list (non-MCP format, for debugging) |
| POST | `/mcp` | MCP Streamable HTTP endpoint (JSON-RPC 2.0) |

## MCP Methods

| Method | Description |
|--------|-------------|
| `initialize` | Returns server info and capabilities (protocol version `2025-03-26`) |
| `notifications/initialized` | Client ack, no response |
| `tools/list` | Returns all discovered tools + 3 builtin meta-tools |
| `tools/call` | Executes a tool by name, routing to the appropriate plugin |
| `ping` | Returns empty result |

## Events

**Subscribes to:**
- `alias:update` — hot-swaps the alias map and invalidates the tool cache (2s debounce)

**Emits:**
- `mcp_server:enabled` — on startup, with `{"endpoint": "http://host:port/mcp"}`
- `mcp_server:disabled` — on shutdown

## How It Works

### Tool discovery

1. Queries kernel for all plugins with `tool:*` capabilities
2. For each plugin, calls `GET /tools` to get its tool definitions
3. Maps tools through aliases:
   - **Tool aliases** (image/video/storage): creates `alias__toolname` entries (e.g. `nb2__generate_image`)
   - **Agent aliases**: creates `alias__chat` entries with a chat-formatted tool
   - **Uncovered plugins**: creates `pluginID__toolname` entries as fallback
4. Results are cached with a 60-second TTL

### Builtin meta-tools

| Tool | Description |
|------|-------------|
| `list_agents` | Queries kernel for `agent:chat` plugins |
| `list_tools` | Returns all discovered tool names and descriptions |
| `send_message` | Routes a chat message to any agent by ID or alias |

### Tool execution

When `tools/call` is invoked:
1. Looks up the tool by `full_name` in the discovered tools cache
2. For `chat` tools: builds a proper `agentChatRequest` with identity system prompt and alias model
3. For regular tools: forwards the arguments JSON as the POST body
4. Routes via `sdk.RouteToPlugin` to the target plugin

### Media handling

Tool responses containing `image_data` (base64) are automatically stored to `storage-sss3` under `media/generated/{uuid}.{ext}` and replaced with a `{{media:key}}` marker to avoid putting large base64 blobs in the LLM context. Video URLs become `{{media_url:...}}` markers.

### TLS support

Supports mTLS when configured via SDK TLS settings (`TLSCert`, `TLSKey`).

## Gotchas / Notes

- The tool cache is global (package-level `var cache`), not per-server instance.
- `InvalidateCache()` is called when aliases change, forcing re-discovery on the next request.
- Agent chat tools inject a system prompt identifying the agent and listing all other available agents/tools — this can be large.
- The `send_message` builtin resolves aliases before routing, so you can use alias names as `agent_id`.
- Tool execution has a 120-second timeout, while the tool cache TTL is 60 seconds — long-running tool calls won't be interrupted by cache refresh.
- The MCP protocol version is `2025-03-26`.
