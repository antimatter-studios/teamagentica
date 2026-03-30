# infra-mcp-server

Model Context Protocol server that exposes platform tools and agent routing to MCP-capable AI clients (Claude Desktop, Cursor, etc.).

## How It Works

Implements the MCP Streamable HTTP transport via mcp-go. On startup, discovers all `tool:*` plugins and maps their tools through the alias system. Plugins can also push-register tools via `POST /tools/register`. The server presents everything as MCP tools alongside 3 builtin meta-tools.

### Tool Discovery

Two paths:

1. **Push-based** -- plugins call `POST /tools/register` with their tool definitions (preferred).
2. **Pull-based fallback** -- for plugins that haven't pushed, queries `GET /mcp` on each `tool:*` plugin.

Discovered tools are cached (60s TTL). Alias-mapped tools get `alias__toolname` naming (e.g. `nb2__generate_image`). Agent aliases get synthetic `alias__chat` tools. Uncovered plugins get `pluginID__toolname` as fallback.

### Builtin Meta-Tools

| Tool | Description |
|------|-------------|
| `list_agents` | Lists all `agent:chat` plugins |
| `list_tools` | Lists all discovered tools with descriptions |
| `send_message` | Routes a chat message to any agent by ID or alias |

### Media Handling

Tool responses containing `image_data` (base64) are stored to `storage-sss3` under `media/generated/{uuid}.{ext}` and replaced with `{{media:key}}` markers. Video URLs become `{{media_url:...}}` markers.

## Capabilities

- `infra:mcp-server`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed MCP request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/info` | Server info: protocol, transport, discovered tool count and names |
| POST, GET, DELETE | `/mcp` | MCP Streamable HTTP endpoint (JSON-RPC 2.0) |
| POST | `/tools/register` | Push-based tool registration from plugins |

## MCP Methods

| Method | Description |
|--------|-------------|
| `initialize` | Server info and capabilities |
| `notifications/initialized` | Client ack, no response |
| `tools/list` | All discovered + builtin tools |
| `tools/call` | Execute a tool, routing to the appropriate plugin |
| `ping` | Returns empty result |

## Events

**Subscribes to:**
- `alias-registry:update` -- hot-swaps alias map, invalidates tool cache (2s debounce)
- `alias-registry:ready` -- re-fetches aliases on registry startup (1s debounce)

**Emits:**
- `mcp_server:enabled` -- on startup, with `{"endpoint": "http://host:port/mcp"}`
- `mcp_server:disabled` -- on shutdown

## Notes

- Tool cache is global (package-level). `InvalidateCache()` forces re-discovery on next request.
- Tool execution has a 120-second timeout.
- Agent chat tools inject a system prompt with agent identity and all available agents/tools.
- `send_message` resolves aliases before routing.
- Supports mTLS when SDK TLS settings are configured.
