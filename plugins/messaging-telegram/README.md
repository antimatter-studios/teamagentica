# messaging-telegram

Telegram bot integration for receiving and responding to messages via long polling or webhook.

## Overview

Connects to the Telegram Bot API using either long polling (default) or webhook mode. Routes all text through `infra-agent-relay` for coordinator/alias resolution. Supports multi-bot mode, user allowlisting, image/video generation via tool aliases, media extraction (photos, video, voice, audio, stickers, documents, locations), and automatic webhook/polling mode switching.

## Capabilities

- `messaging:telegram`
- `messaging:bot`
- `messaging:chat`

## Configuration

| Field | Type | Required | Default | Visible When | Description |
|-------|------|----------|---------|-------------|-------------|
| `BOTS` | bot_token | Yes | -- | Always | JSON array of `{alias, token}` entries. Single = single bot, multiple = multi-bot. |
| `TELEGRAM_MODE` | select | No | `poll` | Always | `poll` or `webhook` |
| `TELEGRAM_POLL_TIMEOUT` | number | No | `60` | Mode = poll | Long poll timeout in seconds |
| `TELEGRAM_WEBHOOK_URL` | string | No | -- | Mode = webhook | Public HTTPS URL for receiving webhook updates |
| `TELEGRAM_ALLOWED_USERS` | string | No | -- | Always | Comma-separated Telegram user IDs. Empty = all users allowed. |
| `PLUGIN_DEBUG` | boolean | No | `false` | Always | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (reports current mode: polling/webhook/idle) |
| `GET` | `/config/options/{field}` | Dynamic config field options |
| `POST` | `/webhook` | Receives Telegram webhook updates |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias:update` | Hot-swaps alias map from registry (2s debounce) |
| `config:update` | Updates message buffer duration at runtime |
| `relay:ready` | Re-emits `relay:coordinator` assignments when relay restarts |
| `webhook:ready` | Sends route info to `network-webhook-ingress` |
| `webhook:plugin:url` | Receives public webhook URL and switches from polling to webhook mode |

**Emits:**

| Event | Addressed? | Description |
|-------|-----------|-------------|
| `relay:coordinator` | Yes (to `infra-agent-relay`) | Maps source plugin to coordinator alias |
| `webhook:api:update` | Yes (to `network-webhook-ingress`) | Registers webhook route prefix |
| `poll_start`, `poll_stop`, `webhook`, `webhook:error` | No | Mode transition events |
| Various (`message_received`, `agent_response`, `error`, etc.) | No | Debug/observability events |

## How It Works

1. **Polling mode (default)** -- Starts long polling via `getUpdates` with configurable timeout. Clears any existing webhook first via `deleteWebhook`. Handles conflict errors (stale poll sessions) with a backoff equal to `pollTimeout`.

2. **Webhook mode** -- When `network-webhook-ingress` sends a `webhook:plugin:url` event, the bot stops polling, calls `setWebhook` on the Telegram API, and switches to receiving updates via `POST /webhook`. Falls back to polling if `setWebhook` fails.

3. **Message buffering** -- Messages are debounced per-chat using `msgbuffer.Buffer` (default 1s). Multiple rapid messages are merged. Configurable via `MESSAGE_BUFFER_MS`.

4. **Relay routing** -- All text goes to `infra-agent-relay` via the relay client. The relay handles @alias parsing, coordinator resolution, persona injection, and workspace routing.

5. **Image/video aliases** -- If `@alias` resolves to an image/video tool, the bot handles it locally: calls the kernel's generation API and sends the result as a native Telegram photo/video.

6. **User allowlisting** -- When `TELEGRAM_ALLOWED_USERS` is set, only listed user IDs can interact. Others are silently blocked with an event emitted.

7. **Media extraction** -- Extracts URLs for photos (highest resolution), video, voice messages, audio files, stickers, and media-type documents. Also checks `ReplyToMessage` so users can reply to an image with a text prompt.

8. **Known chats tracking** -- Persists chat IDs to `/data/known_chats.json`. Used for startup announcements. Stale chats (bot removed) are automatically pruned.

9. **Multi-bot mode** -- Multiple `BOTS` entries each get their own bot instance and source ID. Webhook mode only works for the primary (first) bot.

10. **Commands** -- Registers `/help`, `/clear`, `/aliases` with Telegram's command menu. `/clear` sends a history-clear request to the kernel.

11. **Typing indicator** -- Sends `ChatTyping` action immediately and refreshes every 4 seconds in a loop until the agent responds.

## Runtime Data

### `/data/known_chats.json`

A JSON array of Telegram chat IDs (int64) that the bot has previously received messages from.

```json
[-1001234567890, 9876543210]
```

**Why this exists:** The Telegram Bot API has no endpoint for a bot to list its chats. Bots only learn about chats when they receive messages. This file persists those chat IDs across container restarts so the bot can send startup announcements to all known chats.

**Written when:** A new message arrives from a chat the bot hasn't seen before.

**Read when:** Bot starts up, to send a startup/reconnection announcement.

**Cleanup:** If the bot tries to message a chat and gets a `Forbidden` or `chat not found` error, that chat ID is automatically removed.

## Gotchas / Notes

- **4096-char message limit** -- Telegram caps messages at 4096 characters. Long responses are split at newline/space boundaries.
- **Polling vs webhook** -- Starts in polling mode by default. Switches to webhook automatically when `network-webhook-ingress` provides a URL. Multi-bot mode cannot use webhooks (each bot would need its own URL).
- **Conflict backoff** -- If another instance is polling the same bot token, the bot backs off for `pollTimeout` seconds before retrying (Telegram returns 409 Conflict).
- Supports mTLS for the HTTP server when TLS is configured in the SDK.
- Video generation polls for completion (5s initial, 10s after 30s, 5min timeout) and sends native Telegram video on success, falling back to a link if native send fails.
- Location and venue messages are converted to text descriptions.
