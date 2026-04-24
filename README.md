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

```bash
# Development (hot reload)
task dev:start

# Production
task prod:start
```

First visit: register at the web UI — first user becomes admin.

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
task build:images

# Run kernel with hot reload
cd kernel && air

# Run frontend with HMR
cd user-interface && npm run dev
```

## License

Proprietary — Antimatter Studios
