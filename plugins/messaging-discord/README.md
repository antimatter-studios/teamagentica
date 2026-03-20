# messaging-discord

Discord bot integration for receiving and responding to messages via the Discord gateway.

## Overview

Connects to Discord using the gateway WebSocket API (via discordgo). Responds to DMs and @mentions in guild channels. Routes all text through `infra-agent-relay` for coordinator/alias resolution. Supports multi-bot mode (multiple bot tokens mapped to different aliases), slash command discovery from other plugins, interactive menus, channel management, and image/video generation via tool aliases.

## Capabilities

- `messaging:discord`
- `messaging:bot`
- `messaging:chat`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `BOTS` | bot_token | Yes | -- | JSON array of `{alias, token}` entries. Single entry = single bot, multiple = multi-bot mode. |
| `PLUGIN_DEBUG` | boolean | No | `false` | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/config/options/{field}` | Dynamic config field options |
| `GET` | `/discord-commands` | List registered slash command routes |
| `GET` | `/tools` | List available channel management tools |
| `POST` | `/channels/create-category` | Create a Discord channel category |
| `POST` | `/channels/create` | Create a Discord channel |
| `POST` | `/channels/list` | List Discord channels |
| `POST` | `/channels/delete` | Delete a Discord channel |
| `POST` | `/channels/send-menu` | Send an interactive select menu to a channel |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias:update` | Hot-swaps alias map from registry (2s debounce) |
| `config:update` | Toggles debug mode and message buffer duration at runtime |
| `plugin:registered` | Re-discovers slash commands from all plugins (3s debounce) |
| `relay:ready` | Re-emits `relay:coordinator` assignments when relay restarts |

**Emits:**

| Event | Addressed? | Description |
|-------|-----------|-------------|
| `relay:coordinator` | Yes (to `infra-agent-relay`) | Maps source plugin to coordinator alias |
| Various (`message_received`, `agent_response`, `error`, etc.) | No | Debug/observability events |

## How It Works

1. **Message reception** -- Listens for `MessageCreate` events. Ignores bot messages. Only processes DMs or messages where the bot is @mentioned. Strips the bot mention from the text.

2. **Message buffering** -- Messages are debounced per-channel using `msgbuffer.Buffer` (default 1s). Multiple rapid messages in the same channel are merged before sending to the relay. Configurable via `MESSAGE_BUFFER_MS`.

3. **Relay routing** -- All text goes to `infra-agent-relay` via `relay.Client.Chat(channelID, text, mediaURLs)`. The relay handles @alias parsing, coordinator resolution, persona injection, and workspace routing.

4. **Image/video aliases** -- If `@alias` resolves to a `TargetImage` or `TargetVideo`, the bot handles it locally: calls the kernel's image/video generation API and sends the result as a Discord file attachment or video link.

5. **Slash commands** -- On connect, discovers plugins with `discord:command` capability, reads their `discord_commands` schema, and registers them with Discord. Commands are routed to the owning plugin via `RouteToPlugin`. Supports subcommands, embeds, and text responses.

6. **Interactive menus** -- Other plugins can send select menus via `/channels/send-menu`. Menu selections are routed through the relay as new messages using a callback store.

7. **Multi-bot mode** -- When multiple `BOTS` entries are configured, each gets its own Discord session and source ID (`messaging-discord:{alias}`). Each emits its own `relay:coordinator` event. The first bot is the "primary" used for channel management and slash commands.

8. **Startup announcements** -- On connect (and after significant disconnects >1min), posts a status message to all text channels listing available slash commands and aliases.

9. **Media extraction** -- Extracts image/video/audio URLs from message attachments, embeds, message snapshots (forwarded messages), and referenced messages.

## Gotchas / Notes

- **2000-char message limit** -- Discord caps messages at 2000 characters. Long responses are split at newline/space boundaries. Retry logic handles idle connection resets during long relay calls.
- Slash command discovery retries up to 5 times with increasing delays (0, 3, 5, 10, 15 seconds) since other plugins may not be registered yet at startup.
- The `DISCORD_GUILD_ID` config (read from plugin config but not in schema) is required for channel management and slash command registration.
- Uses `discordgo` library. Intents: GuildMessages, DirectMessages, MessageContent, Guilds.
- Connection loss/resume is tracked; reconnection announcements only fire after >1 minute downtime.
