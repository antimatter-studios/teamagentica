# TeamAgentica

A modular automation control platform with plugin-based AI agents, messaging integrations, cloud workspaces, and a web control panel.

## Features

- **Multi-Agent AI** — Connect Claude, GPT, Gemini, Kimi, and custom models as chat agents with configurable routing via @aliases
- **Messaging Integrations** — Telegram, Discord, WhatsApp bots with message buffering and coordinator delegation
- **Cloud Workspaces** — Browser-based VS Code environments with persistent disks, project detection, and multi-tab management
- **Plugin Marketplace** — Browse, install, and configure plugins from a catalog
- **Tool Plugins** — Image generation (Stability AI, Seedance, NanoBanana), video generation (Veo)
- **Cost Tracking** — Per-model usage analytics with time-windowed pricing
- **Theme System** — 6 switchable themes (Soft Dark, Midnight Blue, Slate, Dracula, High Contrast, Light)
- **Event System** — Real-time inter-plugin pub/sub with broadcast and addressed delivery
- **Security** — JWT auth, RBAC, optional mTLS, audit logging, encrypted secrets

## Architecture

```
┌──────────────┐     ┌──────────┐     ┌──────────────────┐
│   Frontend   │────▶│  Kernel  │────▶│    Plugins (N)    │
│  React/TS    │     │  Go/Gin  │     │  Docker containers│
└──────────────┘     └──────────┘     └──────────────────┘
                          │
                     ┌────┴────┐
                     │ SQLite  │
                     │  + WAL  │
                     └─────────┘
```

- **Kernel** — Minimal control plane: auth, plugin lifecycle, routing, events
- **Plugins** — Each runs in its own Docker container, communicates via REST
- **Frontend** — React SPA, just another API client

## Quick Start

You need Docker, Go, and [chore](https://github.com/antimatter-studios/chore)
(the task runner; it reads `chores.yml`). `tacli` is the command line that
creates and drives an instance — use the released binary, or `go run ./tacli`
from this checkout.

```bash
# 1. Build the kernel and the two plugins every instance boots with
chore kernel:build
chore plugin:build system-user-manager
chore plugin:build system-teamagentica-plugin-provider

# ...and any other plugins you want. `chore plugin:build:all` builds every one,
# which is slow: each plugin is its own Go build.

# 2. Create the instance
cp examples/instance.taconfig my.taconfig    # then edit it
export TA_ADMIN_EMAIL=you@example.com TA_ADMIN_PASSWORD='choose-one'
tacli core create --config my.taconfig

# 3. Check it
tacli status
```

The kernel seeds two system plugins into its database and starts them on every
boot: `system-user-manager` (which owns accounts, so login fails with a 503
until it runs) and `system-teamagentica-plugin-provider`. Their images must
exist locally, or the instance comes up unable to authenticate anyone.

`tacli core create` starts the kernel container, waits for it to become
healthy, creates the admin user, saves a local profile, registers any
marketplace providers, then installs, enables and configures the plugins the
config asks for. Re-running it against an existing database logs in instead of
registering, so it is safe to repeat.

Without `--config` it takes flags instead, which is enough for a bare kernel:

```bash
tacli core create --email you@example.com --password 'choose-one' --port 9741
```

Day to day: `tacli core start|stop|restart|logs`, or the `chore kernel:*`
equivalents. Several instances can coexist on one host: give each its own
`--name`, port and `data_dir`.

Then open the dashboard at the kernel's address and log in with the admin
account you configured.

Two current limits worth knowing:

- The kernel port is published on `127.0.0.1`, so an instance is reachable from
  the machine it runs on. Put a reverse proxy or a tunnel in front of it to
  reach it from elsewhere.
- `tacli` talks to Docker. Other runtimes (Kubernetes, Podman) are not
  supported yet.

## Plugins

### AI Agents
| Plugin | Provider | Capabilities |
|--------|----------|-------------|
| agent-anthropic | Anthropic | agent:chat |
| agent-openai | OpenAI | agent:chat |
| agent-google | Google | agent:chat |
| agent-moonshot | Moonshot | agent:chat |
| agent-inception | Meta-router | agent:chat |
| agent-openrouter | OpenRouter | agent:chat |
| agent-requesty | Requesty | agent:chat |

### Messaging
| Plugin | Platform | Capabilities |
|--------|----------|-------------|
| messaging-chat | Web UI | system:chat |
| messaging-telegram | Telegram | messaging:telegram |
| messaging-discord | Discord | messaging:discord |
| messaging-whatsapp | WhatsApp | messaging:whatsapp |

### Tools
| Plugin | Type | Capabilities |
|--------|------|-------------|
| agent-stability | Image gen | tool:image |
| agent-seedance | Image gen | tool:image |
| agent-nanobanana | Image gen | tool:image |
| agent-veo | Video gen | tool:video |

### Infrastructure
| Plugin | Purpose | Capabilities |
|--------|---------|-------------|
| workspace-manager | Cloud IDE management | workspace:manager |
| infra-cost-explorer | Usage analytics | system:cost-explorer |
| infra-task-scheduler | Scheduled tasks | infra:scheduler |
| infra-mcp-server | MCP protocol | system:mcp |
| network-traffic-manager | Tunnels (ngrok, ssh, ...) | network:ingress, network:tunnel |
| network-webhook-ingress | Webhooks | network:webhook |

### Storage & Workspaces
| Plugin | Purpose | Capabilities |
|--------|---------|-------------|
| storage-sss3 | S3-compatible storage | storage:api, storage:object |
| storage-disk | Disk storage | storage:disk |
| workspace-env-vscode-server | VS Code environment | workspace:environment |
| system-teamagentica-plugin-provider | Plugin catalog | marketplace:provider |

## Documentation

- [Architecture](docs/architecture.md) — System design and component overview
- [Kernel](docs/kernel.md) — API reference and configuration
- [Plugin SDK](docs/plugin-sdk.md) — Building plugins
- [Plugin docs](docs/) — Individual plugin documentation

## Database Standard

All SQLite databases across the kernel and every plugin **must** use the same connection configuration. This ensures consistent behavior for journaling, concurrency, and durability.

### ORM & Driver

- **ORM**: `gorm.io/gorm`
- **Driver**: `gorm.io/driver/sqlite` (wraps `github.com/mattn/go-sqlite3`)
- **Logger**: `logger.Default.LogMode(logger.Warn)`

### DSN Parameters

Append these pragma parameters to every SQLite database path:

```
?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON
```

| Parameter | Value | Purpose |
|-----------|-------|---------|
| `_journal_mode` | `WAL` | Write-Ahead Logging — concurrent reads during writes |
| `_busy_timeout` | `5000` | Wait up to 5s for locked database before returning SQLITE_BUSY |
| `_synchronous` | `NORMAL` | Safe for WAL mode — fsync on checkpoint only |
| `_foreign_keys` | `ON` | Enforce foreign key constraints |

### Example

```go
import (
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

func OpenDB(dbPath string) (*gorm.DB, error) {
    dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"
    return gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Warn),
    })
}
```

### Rules

- **Never** use `database/sql` directly with `go-sqlite3` — always go through GORM
- **Never** use alternative SQLite drivers (e.g. `glebarez/sqlite`) — always use `gorm.io/driver/sqlite`
- **Never** use different journal modes (`DELETE`, `TRUNCATE`) or sync levels (`FULL`, `OFF`)
- **Always** use `AutoMigrate()` for schema management

## Development

```bash
# Build all plugin images
chore plugin:build:all

# Run kernel with hot reload
cd kernel && air

# Run the dashboard with HMR
chore dashboard:dev
```

## License

Proprietary — Antimatter Studios
