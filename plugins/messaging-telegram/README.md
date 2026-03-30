# messaging-telegram

Telegram bot integration for receiving and responding to messages via long polling or webhook.

## Overview

Connects to the Telegram Bot API using either long polling (default) or webhook mode. Routes all text through `infra-agent-relay` for alias resolution, persona injection, and workspace routing. Supports multi-bot mode, user allowlisting, media extraction, forum topics with per-agent channels, and automatic webhook/polling mode switching.

## Capabilities

- `messaging:telegram`
- `messaging:bot`
- `messaging:chat`

**Dependency:** `infra:agent-relay`

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

## Bot Commands

| Command | Description |
|---------|-------------|
| `/help`, `/start` | Show available commands and aliases |
| `/clear`, `/reset` | Clear conversation history |
| `/aliases` | List configured @mention aliases |
| `/newchannel @alias` | Create a dedicated forum topic for an agent |
| `/deletechannel` | Remove agent routing from current topic (or `/deletechannel @alias` from any chat) |
| `/channels` | Show all agent topic mappings in the group |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias-registry:update` | Hot-swaps alias map from registry (2s debounce) |
| `alias-registry:ready` | Loads full alias set when registry starts (1s debounce) |
| `config:update` | Updates message buffer duration at runtime |
| `relay:ready` | Re-emits `relay:coordinator` assignments when relay restarts |
| `relay:progress` | Updates Telegram messages with task/streaming progress |
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

3. **Message buffering** -- Messages are debounced per-chat using `msgbuffer.Buffer` (default 0ms, configurable via `MESSAGE_BUFFER_MS`). Multiple rapid messages are merged.

4. **Relay routing** -- All text goes to `infra-agent-relay` via the relay client. The relay handles @alias parsing, coordinator resolution, persona injection, and workspace routing.

5. **Forum topics** -- In groups with Topics enabled, `/newchannel @alias` creates a dedicated forum topic linked to an agent. Messages in that topic auto-route to the alias without @mention. Mappings persist in a SQLite database (`/data/topics.db`).

6. **User allowlisting** -- When `TELEGRAM_ALLOWED_USERS` is set, only listed user IDs can interact. Others are silently blocked.

7. **Media extraction** -- Extracts URLs for photos (highest resolution), video, voice messages, audio files, stickers, and media-type documents. Checks `ReplyToMessage` so users can reply to an image with a text prompt.

8. **Known chats tracking** -- Persists chat IDs to `/data/known_chats.json` for startup announcements. Stale chats (bot removed) are automatically pruned.

9. **Multi-bot mode** -- Multiple `BOTS` entries each get their own bot instance and source ID. Webhook mode only works for the primary (first) bot.

10. **Typing indicator** -- Sends `ChatTyping` action immediately and refreshes every 4 seconds until the agent responds.

11. **Task progress** -- Handles `relay:progress` events to edit Telegram messages with streaming/task status updates.

## Runtime Data

- `/data/known_chats.json` -- Tracked chat IDs for startup announcements
- `/data/topics.db` -- SQLite database mapping forum topics to agent aliases

## Notes

- 4096-char message limit -- long responses are split at newline/space boundaries.
- Starts in polling mode by default; switches to webhook automatically when ingress provides a URL.
- Conflict backoff -- if another instance polls the same bot token, backs off for `pollTimeout` seconds.
- Supports mTLS when TLS is configured in the SDK.
- Location and venue messages are converted to text descriptions.
