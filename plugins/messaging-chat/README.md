# messaging-chat

Web chat backend providing a REST API for conversations with AI agents. Stores conversations and messages in SQLite, routes messages through `infra-agent-relay`, and exposes MCP tools for programmatic chat access by other agents.

## Capabilities

- `messaging:web`
- `messaging:chat`
- `tool:chat`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | No | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/schema` | Plugin schema |
| POST | `/events` | SDK event handler |
| GET | `/config/options/:field` | Dynamic config field options |
| GET | `/agents` | List available agent aliases |
| GET | `/conversations` | List conversations for authenticated user |
| POST | `/conversations` | Create a new conversation |
| GET | `/conversations/:id` | Get conversation with all messages |
| PUT | `/conversations/:id` | Update conversation (rename) |
| DELETE | `/conversations/:id` | Delete conversation + messages + files |
| POST | `/conversations/:id/read` | Mark conversation as read |
| POST | `/conversations/:id/messages` | Send message, get agent response |
| POST | `/upload` | Upload file attachment (max 10 MB) |
| GET | `/files/*filepath` | Serve file by ID or storage key |

### MCP Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/mcp` | Tool definitions for MCP discovery |
| POST | `/mcp/list_conversations` | List conversations (filterable by user_id) |
| POST | `/mcp/get_messages` | Read messages from a conversation |
| POST | `/mcp/post_message` | Post an assistant message into a conversation |
| POST | `/mcp/create_conversation` | Create a new conversation |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias-registry:update` | Hot-swap alias map (2s debounce) |
| `alias-registry:ready` | Initial alias load (1s debounce) |
| `relay:progress` | Receive task progress updates from relay |
| `config:update` | Receives config changes |

## How It Works

1. Extracts `user_id` from JWT Bearer token. Each conversation is scoped to a user.
2. `POST /conversations/:id/messages` stores the user message, sends to relay, receives agent response (with responder, model, usage, cost), stores assistant message.
3. Channel ID format: `chat:{user_id}:{conversation_id}` for relay routing.
4. File uploads go to local disk. On send, files are base64-encoded as data URLs for the agent. Agent response attachments are stored in sss3.
5. Agent responses may contain `{{media:key}}` or `{{media_url:...}}` references, which are resolved and saved to sss3.
6. Auto-titles conversations on first message (first 50 chars). Deletion cleans up both local files and sss3 keys.
7. MCP tools registered with `infra-mcp-server` when available, allowing other agents to read/write chat conversations.

## Notes

- SQLite (GORM) at `/data/chat.db`. Local to the container.
- Allowed upload MIME types: image (png, jpeg, gif, webp), video (mp4, webm, quicktime), audio (mpeg, ogg, wav, webm, mp4).
- File serving checks local disk first, falls back to sss3 storage.
- Uses Gin for HTTP routing.
