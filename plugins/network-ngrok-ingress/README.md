# network-ngrok-ingress

Creates an ngrok tunnel to expose the webhook plugin to the internet. Auto-discovers the webhook plugin's host:port and broadcasts `ingress:ready` events so other plugins know the public URL.

## Capabilities

- `network:ingress`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `NGROK_AUTHTOKEN` | string | yes (secret) | -- | ngrok auth token |
| `NGROK_DOMAIN` | string | no | -- | Static ngrok domain (e.g. `my-app.ngrok-free.app`) |
| `NGROK_HTTP_PORT` | number | no | `9100` | Port for the health/schema endpoint |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with current tunnel URL |
| GET | `/schema` | Plugin schema (config + status) |
| POST | `/events` | SDK event handler |

## Events

- **Listens:** `webhook:ready` -- auto-discovers webhook plugin host:port as tunnel target
- **Listens:** `ingress:request` -- re-broadcasts `ingress:ready` on demand
- **Listens:** `config:update` -- hot-reloads auth token and domain
- **Emits:** `ingress:ready` -- broadcast with `{url, proto}` when tunnel is established

## How It Works

1. On startup, fetches config and waits for `webhook:ready` event from the webhook plugin
2. Once both auth token and webhook target are known, creates an ngrok tunnel via `ngrok.ListenAndForward()`
3. Broadcasts `ingress:ready` with the public URL so the webhook plugin and others can construct public webhook URLs
4. If config changes at runtime, tears down the old tunnel and creates a new one
5. On shutdown, closes the tunnel gracefully

## Notes

- If `NGROK_AUTHTOKEN` is not set, runs idle (no tunnel)
- Tunnel target is always the webhook plugin -- discovered automatically, not manually configured
- Status schema reports public URL and target (or waiting states)
- Port defaults to 9100
