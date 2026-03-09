# TeamAgentica

A self-hosted, modular AI-orchestrated automation control platform.

## What Is This?

TeamAgentica is a governance-first control plane that coordinates AI agents and plugins while maintaining strict security boundaries. The kernel is minimal and boring — all capabilities come from plugins. AI agents are external clients, not trusted controllers.

The platform enforces authentication, RBAC, audit logging, and policy enforcement across all operations.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   Frontend                      │
│              (React / TypeScript)               │
│        Dashboard · Chat · Marketplace · Debug   │
│                JWT authentication               │
└────────────────────┬────────────────────────────┘
                     │ REST API (HTTP/JSON)
┌────────────────────▼────────────────────────────┐
│                    Kernel                       │
│                    (Go/Gin)                     │
│  ┌──────────┬──────────┬───────────┬──────────┐ │
│  │ JWT Auth │   RBAC   │ Plugin    │  Event   │ │
│  │          │          │ Registry  │  Hub     │ │
│  ├──────────┼──────────┼───────────┼──────────┤ │
│  │ REST API │  Audit   │ Lifecycle │  Route   │ │
│  │          │  Logger  │ Manager   │  Proxy   │ │
│  └──────────┴──────────┴───────────┴──────────┘ │
│            SQLite (GORM) · Docker Runtime       │
└────────────────────┬────────────────────────────┘
                     │ HTTP/REST + Service Tokens
        ┌────────────┼────────────────┐
        ▼            ▼                ▼
   ┌──────────┐ ┌──────────┐   ┌──────────┐
   │  Agent   │ │ Messaging│   │  Tool    │
   │  Plugins │ │  Plugins │   │  Plugins │
   │(AI chat) │ │(Telegram)│   │ (image/  │
   │          │ │(Discord) │   │  video)  │
   └──────────┘ └──────────┘   └──────────┘
```

## Core Principles

1. The kernel is small, stable, and boring
2. All capabilities are implemented as plugins
3. AI agents are external clients, not trusted controllers
4. No component can grant itself additional authority
5. All actions are authenticated, authorized, and audited

## Plugins

TeamAgentica ships with 20+ plugins across seven categories. See [Kernel Documentation](docs/kernel.md) for API reference and [Plugin SDK](docs/plugin-sdk.md) to build your own.

### `agent-*` — AI Model Backends

- [`agent-openai`](docs/plugins/agent-openai.md) — OpenAI GPT-4o, o1 via API key or Codex subscription
- [`agent-claude`](docs/plugins/agent-claude.md) — Anthropic Claude with tool use
- [`agent-gemini`](docs/plugins/agent-gemini.md) — Google Gemini with vision support
- [`agent-kimi`](docs/plugins/agent-kimi.md) — Moonshot Kimi models
- [`agent-openrouter`](docs/plugins/agent-openrouter.md) — OpenRouter multi-model access
- [`agent-requesty`](docs/plugins/agent-requesty.md) — Requesty multi-model router
- [`agent-inception`](docs/plugins/agent-inception.md) — Inception Labs Mercury diffusion LLM

### `messaging-*` — User-Facing Messaging Interfaces

- [`messaging-telegram`](docs/plugins/messaging-telegram.md) — Telegram bot (polling/webhook, media generation)
- [`messaging-discord`](docs/plugins/messaging-discord.md) — Discord bot with alias routing
- [`messaging-whatsapp`](docs/plugins/messaging-whatsapp.md) — WhatsApp Business API bot
- [`messaging-chat`](docs/plugins/messaging-chat.md) — Built-in web chat with conversation history

### `tool-*` — AI Content Generation Tools

- [`tool-stability`](docs/plugins/tool-stability.md) — Stability AI image generation
- [`tool-seedance`](docs/plugins/tool-seedance.md) — Seedance video generation
- [`tool-nanobanana`](docs/plugins/tool-nanobanana.md) — Gemini-powered image generation
- [`tool-veo`](docs/plugins/tool-veo.md) — Google Veo video generation

### `infra-*` — Platform Infrastructure

- [`infra-cost-explorer`](docs/plugins/infra-cost-explorer.md) — AI usage tracking and cost analytics
- [`infra-mcp-server`](docs/plugins/infra-mcp-server.md) — Model Context Protocol server
- [`infra-cron-scheduler`](docs/plugins/infra-cron-scheduler.md) — Cron-style scheduled event system

### `network-*` — Request Routing & Tunneling

- [`network-webhook-ingress`](docs/plugins/network-webhook-ingress.md) — Routes external webhooks to plugins
- [`network-ngrok`](docs/plugins/network-ngrok.md) — Creates public tunnel URLs for webhooks

### `storage-*` — Persistent File Storage

- [`storage-sss3`](docs/plugins/storage-sss3.md) — S3-compatible object storage
- [`storage-volume`](docs/plugins/storage-volume.md) — Local Docker volume storage with tool interface

### `builtin-*` — Required System Plugins

- [`builtin-provider`](docs/plugins/builtin-provider.md) — Default plugin catalog (marketplace)

## Project Structure

```
teamagentica/
├── kernel/              # Go — core API, auth, RBAC, plugin management
├── user-interface/      # React/TS — web dashboard, chat, marketplace
├── plugins/             # 20 plugin implementations (Go)
│   ├── agent-openai/    #   AI agent plugins
│   ├── agent-gemini/
│   ├── messaging-telegram/  #   Messaging plugins
│   ├── discord/
│   ├── messaging-chat/      #   System plugins
│   ├── infra-cost-explorer/
│   └── ...
├── pkg/pluginsdk/       # Shared Go SDK for building plugins
├── docs/                # Architecture docs and planning
├── data/                # Runtime data (database, certs, plugin volumes)
├── docker-compose.yml   # Production deployment
├── docker-compose.dev.yml # Development with hot reload
├── Taskfile.yml         # Build/deploy tasks
└── .env.example         # Configuration template
```

## Quick Start

```bash
# 1. Copy and configure environment
cp .env.example .env
# Edit .env — at minimum set TEAMAGENTICA_JWT_SECRET
# Generate a secret: openssl rand -hex 32

# 2. Start the platform
task prod:start
# Or for development with hot reload:
task dev:start
```

- **Frontend:** http://localhost:3000
- **Kernel API:** http://localhost:9741

On first launch, register a user account. The first user gets admin privileges.

## Development

### Kernel (Go)

```bash
cd kernel
go run .
```

Uses [Air](https://github.com/air-verse/air) for hot reload in dev mode.

### Frontend (React)

```bash
cd user-interface
npm install
npm run dev
```

Vite dev server with HMR at http://localhost:5173.

### Building Plugin Images

```bash
task build:images
```

Each plugin has its own Dockerfile. The kernel launches plugin containers via the Docker API.

## Configuration

### Required

| Variable | Description |
|----------|-------------|
| `TEAMAGENTICA_JWT_SECRET` | JWT signing secret (generate with `openssl rand -hex 32`) |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `TEAMAGENTICA_KERNEL_HOST` | `0.0.0.0` | Kernel bind address |
| `TEAMAGENTICA_KERNEL_PORT` | `8080` | Kernel port |
| `TEAMAGENTICA_KERNEL_ADVERTISE_HOST` | `kernel` | Address plugins use to reach kernel |
| `TEAMAGENTICA_DB_PATH` | `./database.db` | SQLite database path |
| `TEAMAGENTICA_DATA_DIR` | `./data` | Data directory (backups, certs) |
| `TEAMAGENTICA_DOCKER_NETWORK` | `teamagentica_default` | Docker network for plugins |
| `TEAMAGENTICA_MTLS_ENABLED` | `true` | Enable mTLS for plugin communication |
| `APP_NAME` | `TeamAgentica` | Brand name |

### Frontend (build-time)

| Variable | Default | Description |
|----------|---------|-------------|
| `VITE_APP_NAME` | `TeamAgentica` | Brand name in UI |
| `VITE_TEAMAGENTICA_KERNEL_HOST` | `localhost` | Kernel hostname for API calls |
| `VITE_TEAMAGENTICA_KERNEL_PORT` | `9741` | Kernel port for API calls |

## Key Features

### Alias Routing
Configure `@mention` aliases to route messages to specific agents or tools. Messaging bots (Telegram, Discord) use aliases for direct agent selection (`@claude write me a poem`) or coordinator-based delegation where a coordinator agent decides which agent should handle the request.

### Message Buffering
Messaging plugins (Telegram, Discord) buffer rapid sequential messages with a configurable debounce window (default 1000ms). This consolidates multi-part messages (e.g. a forwarded image followed by a text question) into a single agent request. The buffer duration is configurable per-plugin via `MESSAGE_BUFFER_MS` in plugin settings.

### Event System
Plugins communicate through a pub/sub event system. Plugins subscribe to event types and receive HTTP callbacks. Events can be broadcast to all subscribers or addressed to specific plugins.

### Cost Tracking
The infra-cost-explorer plugin aggregates AI usage across all agent plugins. View costs per hour/day/week/month with per-model breakdown. Pricing supports time-effective rates.

### Debug Console
Real-time SSE event stream in the web UI shows all plugin events, errors, and routing decisions as they happen.

### Marketplace
Browse and install plugins from catalog providers. The built-in provider ships a catalog of all available plugins. Plugins declare configuration schemas that generate UI forms automatically.

## Security Model

- **JWT authentication** with capability-encoded tokens
- **RBAC** with admin/user roles and fine-grained capabilities
- **mTLS** (optional) between kernel and plugins with auto-generated CA
- **Service tokens** for plugin-to-kernel authentication
- **Audit logging** of all actions with actor, resource, timestamp, IP
- **Plugin isolation** via Docker containers with dedicated volumes

## Plugin SDK

Build new plugins using the Go SDK:

```go
import "github.com/teamagentica/pkg/pluginsdk"

client := pluginsdk.NewClient(cfg, pluginsdk.Registration{
    PluginID:     "my-plugin",
    Capabilities: []string{"custom:capability"},
    ConfigSchema: []pluginsdk.ConfigSchemaField{...},
})
client.Start(ctx)
defer client.Stop()
```

The SDK handles registration, heartbeats, event subscriptions, alias fetching, and graceful shutdown.

## License

TBD
