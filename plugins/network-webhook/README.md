# network-webhook

Webhook route registry and reverse proxy. Routes external webhook traffic from the ngrok ingress tunnel to internal plugins based on URL path prefixes.

## Capabilities

- `network:webhook`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `WEBHOOK_PORT` | number | no | `9000` | Port for external webhook traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with route count and base URL |
| POST | `/register` | Register a webhook route (plugin_id, prefix, target_host, target_port) |
| POST | `/unregister` | Remove all routes for a plugin |
| GET | `/routes` | List all registered routes (debug) |
| `*` | `/*` | Catch-all: proxy to matched plugin by longest-prefix match |

## Events

- **Listens:** `ingress:ready` -- receives public tunnel URL from ngrok ingress
- **Listens:** `webhook:api:update` -- receives route registrations from gateway plugins (addressed delivery)
- **Listens:** `plugin:registered` -- re-broadcasts `webhook:ready` so late-joining plugins can register
- **Emits:** `webhook:ready` -- broadcast on startup with `{host, port}` so gateway plugins and ngrok can discover this plugin
- **Emits:** `webhook:plugin:url` -- addressed to each plugin with its full public webhook URL
- **Emits:** `webhook:route`, `webhook:register`, `webhook:unregister`, `webhook:error`

## How It Works

1. On startup, broadcasts `webhook:ready` with its host:port (delayed 2s for SDK registration)
2. Gateway plugins (telegram, discord, etc.) send `webhook:api:update` events with their route prefix and internal address
3. Routes are stored in an in-memory table keyed by plugin_id (upsert semantics)
4. When ngrok sends `ingress:ready`, stores the base URL and notifies all registered plugins of their public webhook URLs via `webhook:plugin:url`
5. Inbound HTTP requests are matched by longest-prefix-wins, prefix is stripped, remaining path forwarded to `http://{target_host}:{target_port}/{remaining}`
6. Proxy uses mTLS for outbound requests to backend plugins when TLS config is available

## Notes

- Routes are in-memory only -- plugins must re-register after restart
- Listens on plain HTTP (not mTLS) since it receives external traffic from ngrok
- 30-second proxy timeout
- Schema exposes live status (tunnel URL, route count) and a webhooks section listing all registered plugin routes
- Port defaults to 9000
