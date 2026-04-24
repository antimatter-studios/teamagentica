# network-traffic-manager

Manages N named network tunnels via pluggable drivers. Ships with an `ngrok` driver (feature parity with the old `network-ngrok-ingress` plugin); `ssh` driver planned.

## Capabilities

- `network:ingress`
- `network:tunnel`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `TUNNELS` | string (JSON) | no | -- | JSON array of tunnel specs (see below) |
| `HTTP_PORT` | number | no | `9100` | Port for health + control endpoints |

### Tunnel spec

```json
[
  {
    "name": "ingress",
    "driver": "ngrok",
    "auto_start": true,
    "role": "ingress",
    "target": "webhook",
    "config": {
      "authtoken": "YOUR_NGROK_TOKEN",
      "domain": "my-app.ngrok-free.app"
    }
  }
]
```

| Field | Description |
|-------|-------------|
| `name` | Unique identifier used by start/stop endpoints |
| `driver` | Driver id (`ngrok`, future: `ssh`) |
| `auto_start` | Start automatically once target is resolvable |
| `role` | `ingress` broadcasts `ingress:ready` on start; empty = internal-only |
| `target` | `webhook` (auto-discovered) or literal `host:port` |
| `config` | Driver-specific map |

### Driver configs

**ngrok**
| Key | Required | Description |
|-----|----------|-------------|
| `authtoken` | yes | ngrok auth token |
| `domain` | no | static/reserved ngrok domain |

## API endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health + tunnel snapshot |
| GET | `/tunnels` | List all tunnels with live status |
| GET | `/tunnels/{name}` | Single tunnel status |
| POST | `/tunnels/{name}/start` | Start a tunnel |
| POST | `/tunnels/{name}/stop` | Stop a tunnel |
| GET | `/schema` | Plugin schema |
| POST | `/events` | SDK event handler |

## Events

- **Listens:** `webhook:ready` — resolves the `webhook` target sentinel; auto-starts tunnels that were waiting on it
- **Listens:** `ingress:request` — rebroadcasts `ingress:ready` for the first running `role=ingress` tunnel
- **Listens:** `config:update` — reconciles `TUNNELS` live (diff-apply; untouched tunnels keep running)
- **Emits:** `ingress:ready` — when a tunnel with `role=ingress` starts

## Parity with the old plugin

The original single-ngrok behavior is reproduced by a single spec:

```json
[{"name":"ingress","driver":"ngrok","auto_start":true,"role":"ingress","target":"webhook","config":{"authtoken":"..."}}]
```
