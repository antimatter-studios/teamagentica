# messaging-whatsapp

> WhatsApp Business API bot with alias routing.

## Overview

The WhatsApp plugin connects TeamAgentica to WhatsApp via the WhatsApp Business Cloud API (Meta). It receives messages through Meta's webhook system and routes them to AI agents through the alias system.

## Capabilities

- `messaging:whatsapp` — WhatsApp platform integration

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `WHATSAPP_ACCESS_TOKEN` | string (secret) | yes | — | WhatsApp Business API access token |
| `WHATSAPP_PHONE_NUMBER_ID` | string (secret) | yes | — | WhatsApp phone number ID |
| `WHATSAPP_VERIFY_TOKEN` | string (secret) | yes | — | Webhook verification token |
| `WHATSAPP_APP_SECRET` | string (secret) | no | `""` | App secret for webhook signature verification |
| `PLUGIN_PORT` | int | no | `8091` | HTTP port |
| `PLUGIN_DATA_PATH` | string | no | `/data` | Data directory |
| `DEFAULT_AGENT` | string | no | `""` | Default agent alias |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/webhook` | Meta webhook verification (challenge/response) |
| `POST` | `/webhook` | Meta webhook delivery (returns 200 immediately) |

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Updates `DEFAULT_AGENT`
- `webhook:ready` — Registers route with webhook-ingress
- `webhook:plugin:url` — Receives assigned webhook URL

### Emissions

- `webhook:api:update` — Addressed to network-webhook-ingress
- `webhook_verified`, `message_received`, `agent_response`, `coordinator_delegate`, `alias_route`, `error` — Debug events

## Usage

### Setup

1. Create a Meta App at [developers.facebook.com](https://developers.facebook.com)
2. Set up WhatsApp Business API
3. Configure `WHATSAPP_ACCESS_TOKEN`, `WHATSAPP_PHONE_NUMBER_ID`, and `WHATSAPP_VERIFY_TOKEN`
4. Set the webhook URL in Meta's dashboard (Meta manages registration — no auto-registration)

### Message Types

Handles: text, image (with caption), location, contacts, audio, video, documents.

### Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/start` | Start conversation |
| `/clear`, `/reset` | Clear context |
| `/aliases` | List available aliases |

### Read Receipts

Messages are automatically marked as read via `MarkRead(msg.ID)`.

## Related

- [webhook-ingress](network-webhook-ingress.md) — Webhook URL management
- [ngrok](network-ngrok.md) — Public tunnel for webhooks
- [messaging-telegram](messaging-telegram.md), [messaging-discord](messaging-discord.md) — Other messaging plugins
