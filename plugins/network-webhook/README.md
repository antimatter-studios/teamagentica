# network-webhook-ingress

Public-facing HTTP server that routes external webhook traffic to internal plugins.

## Overview

Acts as a reverse proxy for inbound webhooks. Plugins register route prefixes (e.g. `/telegram-bot`) with their internal host:port. When external traffic arrives via the ngrok tunnel, the ingress matches the URL path against registered prefixes and proxies the request to the correct plugin. Also maintains the tunnel base URL and notifies plugins of their public webhook URLs.

## Capabilities

- `network:webhook`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `WEBHOOK_INGRESS_PORT` | number | no | `9000` | Port to listen on |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with route count and base URL |
| POST | `/register` | Register a webhook route (plugin_id, prefix, target_host, target_port) |
| POST | `/unregister` | Remove routes for a plugin |
| GET | `/routes` | List all registered routes (debug) |
| `*` | `/*` | Catch-all: proxy to matched plugin based on path prefix |

## Events

- **Subscribes:**
  - `webhook:tunnel:update` -- receives tunnel URL from ngrok plugin (addressed delivery)
  - `webhook:api:update` -- receives route registrations from gateway plugins (addressed delivery)
  - `plugin:registered` -- re-broadcasts `webhook:ready` so late-joining plugins can register
- **Emits:**
  - `webhook:ready` -- broadcast on startup so plugins know the ingress is available
  - `webhook:plugin:url` -- addressed to each plugin with its full public webhook URL
  - `webhook:route`, `webhook:register`, `webhook:unregister`, `webhook:tunnel`, `webhook:error`

## How It Works

1. On startup, registers event handlers for tunnel URL updates and route registrations
2. Broadcasts `webhook:ready` with its host:port so gateway plugins (telegram, discord, etc.) can send route registrations
3. When ngrok sends `webhook:tunnel:update`, stores the base URL and notifies all registered plugins of their public URLs
4. When a plugin registers a route (via event or POST), stores it in a prefix-matched route table
5. Inbound HTTP requests are matched against routes by longest-prefix match
6. Matched requests are proxied: prefix is stripped, remaining path forwarded to `http://{target_host}:{target_port}/{remaining}`
7. Headers are forwarded; 30-second proxy timeout

## Gotchas / Notes

- Routes are stored in-memory only -- all plugins must re-register after restart
- The `webhook:ready` broadcast is delayed 2 seconds after startup to let the SDK fully register
- Route matching uses longest-prefix-wins strategy
- Prefix normalization ensures all prefixes start with `/`
- Schema exposes live status (tunnel URL, route count) and a webhooks section listing all registered plugin routes
