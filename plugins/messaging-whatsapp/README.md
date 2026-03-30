# messaging-whatsapp

WhatsApp bot integration using the WhatsApp Business Cloud API. Receives messages via Meta webhooks and routes them through `infra-agent-relay` for agent resolution. Supports text, image, video, audio, location, contact, and document messages.

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
| `PLUGIN_DEBUG` | boolean | No | No | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check (reports whether API credentials are configured) |
| GET | `/schema` | Plugin schema |
| POST | `/events` | SDK event handler |
| GET | `/config/options/:field` | Dynamic config field options |
| GET | `/webhook` | Meta webhook verification (hub.mode, hub.verify_token, hub.challenge) |
| POST | `/webhook` | Incoming WhatsApp messages from Meta |

## Events

**Subscribes to:**

| Event | Description |
|-------|-------------|
| `alias-registry:update` | Hot-swap alias map (2s debounce) |
| `alias-registry:ready` | Initial alias load (1s debounce) |
| `config:update` | Receives config changes |

**Emits:**

| Event | Description |
|-------|-------------|
| `webhook:api:update` | Registers webhook route prefix with `network-webhook-ingress` |

## How It Works

1. Meta sends `GET /webhook` for verification; plugin validates the verify token and echoes the challenge.
2. `POST /webhook` receives messages. Always responds 200 immediately, processes asynchronously.
3. Handles text, image, video, audio (downloaded via Cloud API), location (converted to coordinates text), contacts, and documents.
4. Messages go to `infra-agent-relay` via `relay.Client.Chat()`. Chat ID is the sender's WhatsApp phone number.
5. Marks incoming messages as read via the Cloud API before processing.
6. Registers its webhook route with `network-webhook-ingress` via `sdkClient.RegisterWebhook()`.

## Notes

- No multi-bot mode -- single phone number ID and access token.
- No message buffering -- each message processed independently in its own goroutine.
- Webhook-only -- no polling. URL must be configured in the Meta App Dashboard.
- `WHATSAPP_APP_SECRET` is optional but recommended for production payload signature verification.
- Uses Gin for HTTP routing.
