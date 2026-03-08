# network-ngrok

> Creates public tunnel URLs for webhook delivery.

## Overview

The network-ngrok plugin creates an HTTPS tunnel using the ngrok SDK, exposing an internal service to the internet. It notifies network-webhook-ingress of the tunnel URL so messaging plugins can receive webhooks.

## Capabilities

- `tunnel:ngrok` — Tunnel management

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `NGROK_AUTHTOKEN` | string (secret) | yes | — | ngrok auth token |
| `NGROK_DOMAIN` | string | no | `""` | Static/reserved ngrok domain |
| `NGROK_TUNNEL_TARGET` | string | no | `kernel_host:kernel_port` | Target to tunnel to |
| `NGROK_HTTP_PORT` | int | no | `9100` | Health server port |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (returns tunnel URL) |

## Events

### Emissions

- `webhook:tunnel:update` — Addressed to network-webhook-ingress with `{url, proto: "https"}`

## Usage

### Setup

1. Get an auth token from [ngrok.com](https://ngrok.com)
2. Set `NGROK_AUTHTOKEN` in plugin config
3. Optionally set `NGROK_DOMAIN` for a stable URL

### Tunnel Target

By default, tunnels to the kernel. Set `NGROK_TUNNEL_TARGET` to `network-webhook-ingress:9000` when using the network-webhook-ingress plugin for proper routing.

### Limitations

- No automatic reconnect — if the tunnel dies, the plugin must be restarted
- One tunnel per plugin instance

## Related

- [webhook-ingress](network-webhook-ingress.md) — Routes webhooks to plugins
- [messaging-telegram](messaging-telegram.md) — Uses ngrok for webhook mode
