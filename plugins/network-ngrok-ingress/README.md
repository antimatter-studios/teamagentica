# network-ngrok

Creates an ngrok tunnel for exposing internal services to the internet.

## Overview

Uses the official ngrok Go SDK to create an HTTP tunnel that forwards external traffic to an internal target (defaults to the kernel). On tunnel establishment, sends an addressed event to `network-webhook-ingress` with the public URL so webhook routing can begin. Supports optional custom ngrok domains.

## Capabilities

- `network:ingress`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `NGROK_AUTHTOKEN` | string | yes (secret) | — | ngrok auth token |
| `NGROK_DOMAIN` | string | no | — | Custom static domain (e.g. `my-app.ngrok-free.app`) |
| `NGROK_TUNNEL_TARGET` | string | no | kernel host:port | Internal target to tunnel to |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with current tunnel URL |

## Events

- **Emits:** `webhook:tunnel:update` (addressed to `network-webhook-ingress`) with `{url, proto}`

## How It Works

1. On startup, reads `NGROK_AUTHTOKEN` from config
2. Calls `ngrok.ListenAndForward()` with the auth token, optional domain, and target URL
3. Once the tunnel is established, stores the public URL and sends a `webhook:tunnel:update` addressed event to `network-webhook-ingress`
4. The webhook ingress plugin uses this URL as the base for constructing per-plugin webhook URLs
5. On shutdown, closes the tunnel gracefully

## Gotchas / Notes

- If `NGROK_AUTHTOKEN` is not set, the plugin runs idle (no tunnel created)
- If tunnel creation fails, the plugin continues running but reports the error
- The tunnel target defaults to `{kernel_host}:{kernel_port}` -- set `NGROK_TUNNEL_TARGET` to point at webhook-ingress instead for direct routing
- Status schema reports the public URL (or "(not connected)")
- Only one tunnel per plugin instance
