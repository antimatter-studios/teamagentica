# messaging-chat

Web chat interface for conversing with AI agents via a REST API.

## Overview

Built-in web chat backend that provides a conversation-oriented REST API. Stores conversations and messages in a local SQLite database, routes all messages through `infra-agent-relay`, and serves file attachments from local disk or sss3 storage. Designed to be consumed by a web frontend (e.g. user-vscode-server).

## Capabilities

- `messaging:web`
- `messaging:chat`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | No | `false` | Log detailed request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/config/options/:field` | Dynamic config field options |
| `GET` | `/agents` | List available agent aliases |
| `GET` | `/conversations` | List conversations for authenticated user |
| `POST` | `/conversations` | Create a new conversation |
| `GET` | `/conversations/:id` | Get conversation with all messages |
| `PUT` | `/conversations/:id` | Update conversation (rename) |
| `DELETE` | `/conversations/:id` | Delete conversation, messages, and attached files |
| `POST` | `/conversations/:id/messages` | Send a message and get agent response |
| `POST` | `/upload` | Upload a file attachment (max 10 MB) |
| `GET` | `/files/*filepath` | Serve a file by ID (local disk) or storage key (sss3) |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias:update` | Hot-swaps alias map from registry (2s debounce) |
| `config:update` | Receives config changes (PLUGIN_DEBUG toggle) |

**Emits:** None directly (relay client handles agent communication).

## How It Works

1. **Authentication** -- Extracts `user_id` from a JWT Bearer token in the Authorization header. Each conversation is scoped to a user.

2. **Message flow** -- `POST /conversations/:id/messages` stores the user message in SQLite, sends it to `infra-agent-relay` via the SDK's `RouteToPlugin`, receives the agent response (including responder alias, model, token usage, cost), and stores the assistant message.

3. **Channel ID format** -- Messages are keyed as `chat:{user_id}:{conversation_id}` for relay routing. The relay handles @alias routing, coordinator resolution, persona injection, and conversation memory.

4. **File attachments** -- User uploads go to local disk via `/upload`. On send, files are read and base64-encoded as data URLs for the agent. Agent response attachments (images/video from tools) are decoded and stored in sss3 storage.

5. **Media reference resolution** -- Agent responses may contain `{{media:key}}` (sss3 references) or `{{media_url:...}}` (external/data URLs). These are resolved, saved to sss3, and stripped from the display text.

6. **Alias routing** -- Messages starting with `@alias` are passed through as-is; the relay resolves them. The `/agents` endpoint lists agent-type aliases for the frontend.

7. **Conversation lifecycle** -- Auto-titles on first message (first 50 chars of user text). Deleting a conversation cleans up both local files and sss3 storage keys in the background.

## Gotchas / Notes

- Uses **SQLite** (via GORM) at `/data/chat.db` for conversation history. This is local to the container -- not shared across replicas.
- Allowed MIME types for upload: `image/{png,jpeg,gif,webp}`, `video/{mp4,webm,quicktime}`, `audio/{mpeg,ogg,wav,webm,mp4}`.
- File serving checks local disk first, then falls back to sss3 storage -- supports both legacy file_id attachments and new storage-key attachments.
- No multi-bot mode; single plugin instance per deployment.
- Uses Gin as the HTTP framework (unlike Discord/Telegram which use net/http).
