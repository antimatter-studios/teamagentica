# messaging-discord

Discord bot integration. Connects via the gateway WebSocket API (discordgo), listens for DMs and @mentions, and routes messages through `infra-agent-relay` for agent resolution. Supports multi-bot mode, slash command discovery, channel management, interactive menus, and media attachments.

## Capabilities

- `messaging:discord`
- `messaging:bot`
- `messaging:chat`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `BOTS` | bot_token | Yes | -- | JSON array of `{alias, token}`. Single entry = single bot, multiple = multi-bot. |
| `DISCORD_GUILD_ID` | string | No | auto-detected | Discord server (guild) ID |
| `PLUGIN_DEBUG` | boolean | No | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/schema` | Plugin schema |
| POST | `/events` | SDK event handler |
| GET | `/config/options/{field}` | Dynamic config field options |
| GET | `/discord-commands` | List registered slash commands |
| GET | `/mcp` | Channel management tool definitions |
| POST | `/channels/create-category` | Create a channel category |
| POST | `/channels/create` | Create a channel |
| POST | `/channels/list` | List channels |
| POST | `/channels/delete` | Delete a channel |
| POST | `/channels/send-menu` | Send an interactive select menu |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias-registry:update` | Hot-swap alias map (2s debounce) |
| `alias-registry:ready` | Initial alias load (1s debounce) |
| `config:update` | Toggle debug, message buffer duration |
| `plugin:registered` | Re-discover slash commands (3s debounce) |
| `relay:ready` | Re-emit `relay:coordinator` assignments |
| `relay:progress` | Update Discord messages with task status |

**Emits:**

| Event | Description |
|-------|-------------|
| `relay:coordinator` | Maps source plugin to coordinator alias (addressed to relay) |

## How It Works

1. Listens for `MessageCreate` events. Ignores bot messages. Processes DMs and @mentions only.
2. Messages are debounced per-channel via `msgbuffer.Buffer` (configurable via `MESSAGE_BUFFER_MS`). Multiple rapid messages merge before relay dispatch.
3. All text goes to `infra-agent-relay` via `relay.Client.Chat()`. The relay handles @alias parsing, persona injection, and memory.
4. If an alias resolves to an image/video target, the bot handles it locally via the kernel's generation API and sends the result as a Discord attachment.
5. Discovers plugins with `discord:command` capability and registers slash commands with Discord. Commands route to owning plugins.
6. Other plugins can send select menus via `/channels/send-menu`; selections route through the relay as messages.
7. Multi-bot mode: each `BOTS` entry gets its own Discord session and source ID (`messaging-discord:{alias}`).
8. Startup announcements post to text channels listing available commands and aliases (throttled, only after >1min disconnects).

## Notes

- 2000-char Discord message limit; long responses split at newline/space boundaries.
- Slash command discovery retries up to 5 times (0, 3, 5, 10, 15s delays).
- Intents: GuildMessages, DirectMessages, MessageContent, Guilds.
- Media extracted from attachments, embeds, message snapshots, and referenced messages.
