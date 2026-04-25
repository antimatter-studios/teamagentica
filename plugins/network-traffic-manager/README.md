# network-traffic-manager

Manages N named network tunnels via pluggable drivers. Ships with an `ngrok` driver (feature parity with the old `network-ngrok-ingress` plugin), an `ssh-reverse` driver (reverse port-forward through a user-owned SSH bastion), and an `ssh-jumphost` driver (terminates an SSH session locally and exposes the forwarded ssh-agent over a unix socket).

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

**ssh-reverse** — establishes an `ssh -R` reverse forward through a user-owned public SSH bastion. The bastion listens on `remote_bind_host:remote_bind_port` and pipes inbound connections back to `target` over the SSH session. Requires `GatewayPorts yes` on the bastion if you want remote binds reachable from outside the bastion itself.

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `host` | yes | — | Public SSH bastion hostname/IP |
| `port` | no | `22` | SSH port |
| `user` | yes | — | Bastion SSH user |
| `private_key` | one of | — | OpenSSH private key PEM |
| `password` | one of | — | Password (fallback; prefer key) |
| `remote_bind_host` | no | `0.0.0.0` | Bastion bind host for the remote forward |
| `remote_bind_port` | no | `0` (auto) | Bastion bind port — pick one you've opened in the bastion's firewall |
| `known_hosts` | no | — | Pinned bastion host keys (authorized_keys format); empty = accept any (insecure) |

**ssh-jumphost** — opens a reverse SSH tunnel to a public bastion (same mechanism as `ssh-reverse`) and runs an *embedded* SSH server on the local end of that tunnel. When a user connects with agent forwarding (`ssh -A user@bastion:port`), the forwarded ssh-agent is exposed as a Unix socket at `agent_socket_path` for consumption by other plugins (e.g. workspaces mounting the `agent-sockets` shared disk via `SSH_AUTH_SOCK`). The driver's `target` field is unused for this driver — the conceptual target is `agent_socket_path`.

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `bastion_host` | yes | — | Public SSH bastion hostname/IP |
| `bastion_port` | no | `22` | Bastion SSH port |
| `bastion_user` | yes | — | Bastion SSH user |
| `bastion_private_key` | yes | — | OpenSSH PEM private key for bastion auth |
| `bastion_known_hosts` | no | — | Pinned bastion host keys (authorized_keys format); empty = accept any (insecure) |
| `bastion_remote_bind_host` | no | `0.0.0.0` | Bastion bind host for the remote forward |
| `bastion_remote_bind_port` | no | `0` (auto) | Bastion bind port — pick one you've opened in the bastion's firewall |
| `username` | yes | — | The only username allowed to authenticate to the embedded SSH server |
| `authorized_keys` | yes | — | Newline-separated authorized_keys-format pubkeys; only these may auth |
| `agent_socket_path` | yes | — | Path where the forwarded ssh-agent is exposed as a Unix socket |
| `host_key` | no | — | Inline OpenSSH PEM for the embedded server's host key |
| `host_key_path` | no | — | Filesystem path to load/save the host key (loads if exists, otherwise generates ed25519 + persists 0600). Use this to keep a stable identity across restarts. Ignored if `host_key` is set. |
| `socket_mode` | no | `0666` | Octal permissions applied to the agent unix socket — keep it open enough for the consuming workspace to read it across UID boundaries |

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
