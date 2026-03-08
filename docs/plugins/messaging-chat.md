# messaging-chat

> Web chat orchestration with conversation history, agent routing, and media handling.

## Overview

The chat plugin powers the web UI chat experience. It manages conversations, routes messages to AI agents via aliases, handles coordinator delegation, and processes media attachments. Authenticated via JWT — each user sees only their own conversations.

## Capabilities

- `system:chat` — Core chat orchestration

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `DEFAULT_AGENT` | select (dynamic) | no | `""` | Default agent alias for unaddressed messages |
| `PLUGIN_DATA_PATH` | string | no | `/data` | Data directory |
| `PLUGIN_PORT` | int | no | `8092` | HTTP port |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/config/options/:field` | Dynamic options (e.g., agent list for `DEFAULT_AGENT`) |
| `GET` | `/agents` | List available agents from alias map |
| `GET` | `/conversations` | List user's conversations (JWT auth) |
| `POST` | `/conversations` | Create conversation |
| `GET` | `/conversations/:id` | Get conversation with messages |
| `PUT` | `/conversations/:id` | Update conversation |
| `DELETE` | `/conversations/:id` | Delete conversation (cascades to files + storage) |
| `POST` | `/conversations/:id/messages` | Send message to agent |
| `POST` | `/upload` | Upload image (PNG/JPEG/GIF/WEBP, max 10MB) |
| `GET` | `/files/*filepath` | Serve files (local first, falls back to sss3 storage) |

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Updates `DEFAULT_AGENT` setting

## Usage

### Message Routing

1. **`@alias` prefix**: Routes directly to the named agent
2. **No prefix**: Routes to coordinator agent (or `DEFAULT_AGENT`)
3. **Coordinator delegation**: If coordinator responds with `DELEGATE:@alias:msg`, re-routes to that agent

### Media Handling

Agent responses can contain markers:
- `{{media:storage/key}}` — Rendered as media from sss3 storage
- `{{media_url:https://...}}` — Rendered as external media URL

These are parsed into structured `Attachment` objects with `mime_type` and `image_data` or `url` fields.

### Conversation Context

The last 80 messages are sent as conversation context to the agent.

## Related

- [Plugin SDK](../plugin-sdk.md) — SDK reference
- [storage-sss3](storage-sss3.md) — File storage backend
- [messaging-telegram](messaging-telegram.md), [messaging-discord](messaging-discord.md), [messaging-whatsapp](messaging-whatsapp.md) — Messaging alternatives
