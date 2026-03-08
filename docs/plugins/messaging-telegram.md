# messaging-telegram

> Telegram bot with polling/webhook modes, alias routing, and media generation support.

## Overview

The Telegram plugin connects TeamAgentica to Telegram. It supports both long-polling and webhook modes, routing messages to AI agents through aliases. Handles text, photos, locations, and supports image/video generation with async polling and typing indicators.

## Capabilities

- `messaging:telegram` — Telegram platform integration
- `messaging:send` — Can send messages
- `messaging:receive` — Can receive messages

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | string (secret) | yes | — | Telegram bot token from @BotFather |
| `TELEGRAM_MODE` | select | no | `poll` | `"poll"` or `"webhook"` |
| `TELEGRAM_POLL_TIMEOUT` | int | no | `60` | Polling timeout seconds (visible when mode=poll) |
| `TELEGRAM_WEBHOOK_URL` | string | no | `""` | Webhook URL (visible when mode=webhook) |
| `TELEGRAM_ALLOWED_USERS` | string | no | `""` | Comma-separated Telegram user IDs to allow |
| `TELEGRAM_HTTP_PORT` | int | no | `8443` | HTTP port |
| `DEFAULT_AGENT` | string | no | `""` | Default agent alias |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (includes current mode) |
| `GET` | `/config/options/:field` | Dynamic agent list for DEFAULT_AGENT |
| `POST` | `/webhook` | Telegram webhook delivery |

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Updates `DEFAULT_AGENT`
- `webhook:ready` — Registers route with network-webhook-ingress
- `webhook:plugin:url` — Transitions from polling to webhook mode

### Emissions

- `webhook:api:update` — Addressed to network-webhook-ingress with route prefix
- `poll_start`, `poll_stop`, `webhook`, `webhook:error` — Debug events

## Usage

### Setup

1. Create a bot via [@BotFather](https://t.me/BotFather) on Telegram
2. Set `TELEGRAM_BOT_TOKEN` in plugin config
3. Optionally set `TELEGRAM_ALLOWED_USERS` to restrict access

### Modes

- **Polling** (default): Long-polls Telegram servers. No public URL needed. Clears existing webhooks on start.
- **Webhook**: Requires a public URL (via ngrok or custom). Auto-transitions when `webhook:plugin:url` event arrives from network-webhook-ingress.

### Commands

| Command | Description |
|---------|-------------|
| `/clear` | Clear conversation context |
| `/aliases` | List available aliases |
| `/help` | Show help |

### Message Types

Handles: text, photos (highest resolution downloaded), captions, locations, venues.

### Media Generation

- **Image** (`alias.TargetImage`): Routes to image tool, sends result as photo
- **Video** (`alias.TargetVideo`): Async polling (5s initial, 10s later, max 5min), notifies at 30s delay

### Message Splitting

Telegram has a 4096-character limit. Long responses are automatically split.

### Response Attribution

Responses include `[@alias]` prefix to show which agent replied.

### Typing Indicator

Sends "typing" action every 4s while waiting for agent response.

## Related

- [webhook-ingress](network-webhook-ingress.md) — Webhook URL management
- [ngrok](network-ngrok.md) — Public tunnel for webhooks
- [messaging-discord](messaging-discord.md), [messaging-whatsapp](messaging-whatsapp.md) — Other messaging plugins
