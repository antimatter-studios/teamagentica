# messaging-discord

> Discord bot with alias routing, message buffering, forwarded message support, and response attribution.

## Overview

The Discord plugin connects TeamAgentica to Discord via a bot. It responds to DMs and @mentions in guild channels, routing messages to AI agents through the alias system. Supports coordinator delegation for automatic agent selection, image/video generation, and forwarded message media extraction. Sequential messages are buffered with a configurable debounce window.

## Capabilities

- `messaging:discord` — Discord platform integration
- `messaging:send` — Can send messages
- `messaging:receive` — Can receive messages

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `BOTS` | bot_token | yes | — | Bot identities — each maps an alias to a Discord bot token. Single entry = single bot, multiple = multi-bot mode. |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/config/options/:field` | Dynamic config options |

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Updates `MESSAGE_BUFFER_MS`, `PLUGIN_DEBUG`
- `relay:ready` — Re-emits coordinator assignments when relay restarts
- `plugin:registered` — Refreshes slash commands

## Usage

### Setup

1. Create a Discord bot at [discord.com/developers](https://discord.com/developers)
2. Enable intents: Guild Messages, Direct Messages, Message Content
3. Add bot identity in `BOTS` config (alias + token)
4. Invite bot to your server with message read/send permissions

### Message Routing

- **@alias prefix**: `@claude write a poem` → routes to the `claude` agent
- **Direct mention or DM**: Routes to coordinator (via infra-agent-relay)
- **Coordinator delegation**: Coordinator can delegate via `ROUTE:@alias\nmessage`

### Message Buffering

Sequential messages from the same channel are buffered for the configured debounce window (default 1000ms). When the window expires, all buffered messages are merged:

- Text from multiple messages is joined with newlines
- Media URLs are deduplicated and combined
- Handles the common pattern of forwarding an image then typing a follow-up question

### Media Extraction

The bot extracts media URLs from multiple sources:

- **Attachments**: Image, video, and audio attachments (checked by `ContentType`, with filename extension fallback for empty content types)
- **Embeds**: Image and thumbnail URLs from message embeds
- **Message Snapshots**: Forwarded messages include snapshots of the original message; attachments and embeds are extracted from these snapshots
- **Referenced Messages**: If a forwarded message's snapshot lacks attachment data, the bot fetches the original message via the Discord API using the `MessageReference` to get full attachment details

### Forwarded Messages

When a user forwards a message containing an image:
1. The bot checks `MessageSnapshots` for attachments and embeds
2. If no media found, falls back to fetching the original message via `MessageReference`
3. Forwarded message text is used as content if the user didn't add their own text
4. Debug logging shows snapshot contents and attachment details for diagnostics

### Response Attribution

All responses include `[@alias]` prefix to show which agent replied. This applies to:
- Coordinator responses (attributed to the coordinator's alias or plugin ID)
- Direct alias routing (attributed to the target alias)
- Delegated responses (attributed to the delegated alias)

### Message Splitting

Discord has a 2000-character limit. Long responses are automatically split at newline or space boundaries.

### Media Generation

- **Image** (`alias.TargetImage`): Routes to image tool, sends result as Discord file attachment
- **Video** (`alias.TargetVideo`): Async polling (5s initial, 10s later, max 5min), sends video link on completion

## Related

- [Plugin SDK](../plugin-sdk.md) — Alias integration
- [messaging-telegram](messaging-telegram.md), [messaging-whatsapp](messaging-whatsapp.md) — Other messaging plugins
