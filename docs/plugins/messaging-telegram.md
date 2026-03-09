# messaging-telegram

> Telegram bot with polling/webhook modes, alias routing, message buffering, and media generation support.

## Overview

The Telegram plugin connects TeamAgentica to Telegram. It supports both long-polling and webhook modes, routing messages to AI agents through aliases. Handles text, photos, videos, voice, audio, documents, stickers, locations, and supports image/video generation with async polling and typing indicators. Sequential messages are buffered with a configurable debounce window to consolidate multi-part messages (e.g. forwarded image + follow-up text) into a single agent request.

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
| `DEFAULT_AGENT` | select | no | `""` | Coordinator agent alias (dynamic options) |
| `MESSAGE_BUFFER_MS` | number | no | `1000` | Debounce window in ms for consolidating sequential messages. Set to 0 to disable. |
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
- `config:update` — Updates `DEFAULT_AGENT`, `MESSAGE_BUFFER_MS`, `PLUGIN_DEBUG`
- `webhook:ready` — Registers route with network-webhook-ingress
- `webhook:plugin:url` — Transitions from polling to webhook mode

### Emissions

- `webhook:api:update` — Addressed to network-webhook-ingress with route prefix
- `poll_start`, `poll_stop`, `webhook`, `webhook:error` — Debug events
- `update_skipped` — Debug: non-message update received (shows update type)
- `forward_debug` — Debug: forwarded message details (photo/video/doc presence, media count)

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

Commands bypass the message buffer and are handled immediately.

### Message Buffering

Sequential messages from the same chat are buffered for the configured debounce window (default 1000ms). When the window expires without new messages, all buffered messages are merged and sent as a single request:

- Text from multiple messages is joined with newlines
- Media URLs are deduplicated and combined
- This handles the common pattern of forwarding an image then typing a follow-up question

### Message Types

Handles: text, photos (highest resolution), captions, videos, voice messages, audio files, documents (image/video/audio MIME types), stickers, locations, venues.

### Forwarded Messages

Forwarded messages are handled like regular messages — photos, videos, and other media are extracted normally. Debug events (`forward_debug`) log the media presence for diagnostics. Note: forwarded channel posts may have `From` as nil, which is handled safely.

### Reply-to-Message Media

When a user replies to a message containing media (e.g. replying to a photo with a text question), the bot extracts media from both the reply and the original message. This allows patterns like:
1. Bot sends an AI-generated image
2. User replies with "Can you describe what's in this image?"
3. Bot sends both the image URL and the user's text to the agent

### Media Generation

- **Image** (`alias.TargetImage`): Routes to image tool, sends result as photo
- **Video** (`alias.TargetVideo`): Async polling (5s initial, 10s later, max 5min), notifies at 30s delay

### Message Splitting

Telegram has a 4096-character limit. Long responses are automatically split at newline or space boundaries.

### Response Attribution

Responses include `[@alias]` prefix to show which agent replied. This applies to coordinator responses, direct alias routing, and delegated responses.

### Typing Indicator

Sends "typing" action every 4s while waiting for agent response.

## Related

- [webhook-ingress](network-webhook-ingress.md) — Webhook URL management
- [ngrok](network-ngrok.md) — Public tunnel for webhooks
- [messaging-discord](messaging-discord.md), [messaging-whatsapp](messaging-whatsapp.md) — Other messaging plugins
