# messaging-discord

> Discord bot with alias routing and multi-agent support.

## Overview

The Discord plugin connects TeamAgentica to Discord via a bot. It responds to DMs and @mentions in guild channels, routing messages to AI agents through the alias system. Supports coordinator delegation for automatic agent selection.

## Capabilities

- `messaging:discord` — Discord platform integration
- `messaging:send` — Can send messages
- `messaging:receive` — Can receive messages

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `TEAMAGENTICA_DISCORD_TOKEN` | string (secret) | yes | — | Discord bot token |
| `DEFAULT_AGENT` | string | no | `""` | Default agent alias |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

None — this is an event-only plugin with no HTTP server.

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Updates `DEFAULT_AGENT`

## Usage

### Setup

1. Create a Discord bot at [discord.com/developers](https://discord.com/developers)
2. Enable intents: Guild Messages, Direct Messages, Message Content
3. Set `TEAMAGENTICA_DISCORD_TOKEN` in plugin config
4. Invite bot to your server with message read/send permissions

### Message Routing

- **@alias prefix**: `@claude write a poem` → routes to the `claude` agent
- **Direct mention or DM**: Routes to coordinator or `DEFAULT_AGENT`
- **Coordinator delegation**: Coordinator can delegate via `DELEGATE:@alias:msg`

### Message Splitting

Discord has a 2000-character limit. Long responses are automatically split at newline or space boundaries.

### Auto-Discovery

If no `DEFAULT_AGENT` is set and no coordinator alias exists, the bot auto-discovers the first `ai:chat` plugin via `FindAIAgent()`.

## Related

- [Plugin SDK](../plugin-sdk.md) — Alias integration
- [messaging-telegram](messaging-telegram.md), [messaging-whatsapp](messaging-whatsapp.md) — Other messaging plugins
