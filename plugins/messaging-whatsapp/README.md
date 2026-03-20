# messaging-whatsapp

WhatsApp bot integration using the WhatsApp Business Cloud API.

## Overview

Receives messages via Meta's webhook system and responds through the WhatsApp Business Cloud API. Routes all text through `infra-agent-relay` for coordinator/alias resolution. Supports text, image, video, audio, location, contact, and document message types. Uses Gin for HTTP routing.

## Capabilities

- `messaging:whatsapp`
- `messaging:bot`
- `messaging:chat`

## Configuration

| Field | Type | Required | Secret | Description |
|-------|------|----------|--------|-------------|
| `WHATSAPP_ACCESS_TOKEN` | string | Yes | Yes | WhatsApp Business API access token |
| `WHATSAPP_PHONE_NUMBER_ID` | string | Yes | No | WhatsApp Business phone number ID |
| `WHATSAPP_VERIFY_TOKEN` | string | Yes | No | Token for Meta webhook verification handshake |
| `WHATSAPP_APP_SECRET` | string | No | Yes | App secret for payload signature verification |
| `PLUGIN_DEBUG` | boolean | No | No | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (reports whether API credentials are configured) |
| `GET` | `/config/options/:field` | Dynamic config field options |
| `GET` | `/webhook` | Meta webhook verification (hub.mode, hub.verify_token, hub.challenge) |
| `POST` | `/webhook` | Receives incoming WhatsApp messages from Meta |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias:update` | Hot-swaps alias map from registry (2s debounce) |
| `config:update` | Receives config changes |
| `webhook:ready` | Sends route info to `network-webhook-ingress` |
| `webhook:plugin:url` | Logs the assigned public webhook URL (Meta manages actual registration) |

**Emits:**

| Event | Addressed? | Description |
|-------|-----------|-------------|
| `webhook:api:update` | Yes (to `network-webhook-ingress`) | Registers webhook route prefix |
| `webhook_verified`, `message_received`, `agent_response`, `error` | No | Debug/observability events |

## How It Works

1. **Webhook verification** -- Meta sends `GET /webhook?hub.mode=subscribe&hub.verify_token=TOKEN&hub.challenge=CHALLENGE`. The plugin validates the verify token and echoes the challenge back.

2. **Message reception** -- Meta sends `POST /webhook` with a payload containing entries/changes/messages. The plugin always responds 200 immediately (to avoid Meta retries), then processes messages asynchronously in goroutines.

3. **Content extraction** -- Handles multiple message types:
   - `text` -- plain text body
   - `image` -- downloads media via Cloud API, sends URL + caption to agent
   - `video` -- downloads media, sends URL + caption
   - `audio` -- downloads media, sends URL
   - `location` -- converts to text description with coordinates
   - `contacts` -- converts to text with name and phone
   - `document` -- converts to text with filename

4. **Relay routing** -- All messages go to `infra-agent-relay` via `relay.Client.Chat(chatID, text, imageURLs)`. The relay handles @alias routing, coordinator resolution, persona injection, and conversation memory. The chat ID is the sender's WhatsApp phone number.

5. **Commands** -- `/help` and `/start` show available commands. `/aliases` lists configured @mention aliases.

6. **Read receipts** -- Marks incoming messages as read via the Cloud API before processing.

## Gotchas / Notes

- **No multi-bot mode** -- Unlike Discord/Telegram, WhatsApp uses a single phone number ID and access token. No bot_token array config.
- **No message buffering** -- Unlike Discord/Telegram, there is no `msgbuffer` debounce. Each message is processed independently in its own goroutine.
- **Webhook-only** -- WhatsApp Business API only supports webhooks, not polling. The webhook URL must be configured in the Meta App Dashboard. The `webhook:plugin:url` event only logs the URL for visibility.
- **Media download** -- Images, video, and audio are downloaded via the WhatsApp Cloud API (`DownloadMedia`) which returns a temporary URL. This URL is passed to the agent as an image URL.
- **No message splitting** -- Unlike Discord (2000 chars) and Telegram (4096 chars), WhatsApp responses are sent as a single `SendText` call without chunking.
- **WHATSAPP_APP_SECRET** is optional but recommended for verifying webhook payload signatures in production.
- Sender names are resolved from the `contacts` array in the webhook payload.
