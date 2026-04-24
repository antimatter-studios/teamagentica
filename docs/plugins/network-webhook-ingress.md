# network-webhook-ingress

> Routes external webhooks to plugins via reverse proxy with longest-prefix matching.

## Overview

The network-webhook-ingress plugin acts as a reverse proxy for incoming webhooks. It maintains a routing table of URL prefixes → plugin targets and forwards matching requests. Works with ngrok to provide public URLs to messaging plugins.

## Capabilities

- `webhook:ingress` — Webhook routing
- `webhook:routing` — Route management

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `WEBHOOK_INGRESS_PORT` | int | no | `9000` | HTTP port |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (route count + base URL) |
| `POST` | `/register` | Register route: `{plugin_id, prefix, target_host, target_port}` |
| `POST` | `/unregister` | Remove route: `{plugin_id}` |
| `GET` | `/routes` | List all routes (debug) |
| `/*` | catch-all | Reverse proxy: longest-prefix match → forward to target |

## Events

### Subscriptions

- `webhook:tunnel:update` — Sets `baseURL` from ngrok tunnel; notifies all registered plugins
- `webhook:api:update` — Upserts a route from a plugin; notifies that plugin with its URL
- `plugin:registered` — Re-broadcasts `webhook:ready` so late-joining plugins can register

### Emissions

- `webhook:ready` — Broadcast on startup (after 2s delay) and on new plugin registrations
- `webhook:plugin:url` — Addressed to plugin with its public webhook URL

## Usage

### Coordination Flow

1. **network-traffic-manager** starts its `role=ingress` tunnel → broadcasts `ingress:ready`
2. **network-webhook-ingress** sets base URL → broadcasts `webhook:ready`
3. **Messaging plugin** receives `webhook:ready` → sends `webhook:api:update` with its route
4. **network-webhook-ingress** registers route → sends `webhook:plugin:url` back with the full public URL
5. **Messaging plugin** uses the URL to configure its platform webhook

### Route Matching

- Longest-prefix match among all registered prefixes
- Prefix is stripped from the forwarded request path
- Query strings are preserved
- 30s proxy timeout

## Related

- [network-traffic-manager](network-traffic-manager.md) — Provides the public tunnel URL via ngrok driver
- [messaging-telegram](messaging-telegram.md), [messaging-whatsapp](messaging-whatsapp.md) — Plugins that use webhook-ingress
