# network-traffic-manager

> Manages N named network tunnels via pluggable drivers (ngrok today, ssh planned).

## Overview

`network-traffic-manager` replaces the single-purpose `network-ngrok-ingress` plugin. Instead of one hard-wired ngrok tunnel, it maintains a list of named tunnels — each with its own driver, target, role, and driver-specific config. Tunnels can be auto-started from config or controlled at runtime via the HTTP API. Tunnels with `role=ingress` broadcast `ingress:ready` for webhook consumers.

## Capabilities

- `network:ingress` — public URL delivery for webhook-consuming plugins
- `network:tunnel` — generic named-tunnel management

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `TUNNELS` | string (JSON) | no | `""` | JSON array of tunnel specs |
| `HTTP_PORT` | int | no | `9100` | Port for health + control endpoints |

### Tunnel spec fields

| Field | Description |
|-------|-------------|
| `name` | Unique identifier (used by start/stop endpoints) |
| `driver` | Driver id (`ngrok`; future: `ssh`) |
| `auto_start` | Start automatically once target is known |
| `role` | `ingress` broadcasts `ingress:ready`; empty = internal-only |
| `target` | `webhook` (auto-discovered) or literal `host:port` |
| `config` | Driver-specific map (see below) |

### Driver configs

**ngrok**
- `authtoken` *(required)* — ngrok auth token
- `domain` *(optional)* — static/reserved domain

### Parity with old network-ngrok-ingress

```json
[{"name":"ingress","driver":"ngrok","auto_start":true,"role":"ingress","target":"webhook","config":{"authtoken":"...","domain":"..."}}]
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health + all tunnel snapshots |
| GET | `/tunnels` | List tunnels with live status |
| GET | `/tunnels/{name}` | Single tunnel status |
| POST | `/tunnels/{name}/start` | Start a tunnel by name |
| POST | `/tunnels/{name}/stop` | Stop a tunnel by name |
| GET | `/schema` | Plugin schema |

## Events

- **Listens:** `webhook:ready` — resolves `target: "webhook"` specs; auto-starts them once the webhook plugin is discovered
- **Listens:** `ingress:request` — rebroadcasts `ingress:ready` from the first running `role=ingress` tunnel
- **Listens:** `config:update` — diff-applies `TUNNELS` live (unchanged tunnels keep running)
- **Emits:** `ingress:ready` — when a `role=ingress` tunnel starts

## Related

- [network-webhook-ingress](network-webhook-ingress.md) — consumes `ingress:ready`
- [messaging-telegram](messaging-telegram.md) — uses public URL for webhook mode
