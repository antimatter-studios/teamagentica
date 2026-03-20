# TeamAgentica

A modular automation control platform with plugin-based AI agents, messaging integrations, cloud workspaces, and a web control panel.

## Features

- **Multi-Agent AI** — Connect Claude, GPT, Gemini, Kimi, and custom models as chat agents with configurable routing via @aliases
- **Messaging Integrations** — Telegram, Discord, WhatsApp bots with message buffering and coordinator delegation
- **Cloud Workspaces** — Browser-based VS Code environments with persistent volumes, project detection, and multi-tab management
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
| agent-claude | Anthropic | agent:chat |
| agent-openai | OpenAI | agent:chat |
| agent-gemini | Google | agent:chat |
| agent-kimi | Moonshot | agent:chat |
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
| tool-stability | Image gen | tool:image |
| tool-seedance | Image gen | tool:image |
| tool-nanobanana | Image gen | tool:image |
| tool-veo | Video gen | tool:video |

### Infrastructure
| Plugin | Purpose | Capabilities |
|--------|---------|-------------|
| infra-workspace-manager | Cloud IDE management | workspace:manager |
| infra-cost-explorer | Usage analytics | system:cost-explorer |
| infra-cron-scheduler | Scheduled tasks | system:cron |
| infra-mcp-server | MCP protocol | system:mcp |
| network-ngrok | Tunnel | network:tunnel |
| network-webhook-ingress | Webhooks | network:webhook |

### Storage & Workspaces
| Plugin | Purpose | Capabilities |
|--------|---------|-------------|
| storage-sss3 | S3-compatible storage | storage:api, storage:object |
| storage-volume | Block storage | storage:volume |
| user-vscode-server | VS Code environment | workspace:environment |
| builtin-provider | Plugin catalog | marketplace:provider |

## Documentation

- [Architecture](docs/architecture.md) — System design and component overview
- [Kernel](docs/kernel.md) — API reference and configuration
- [Plugin SDK](docs/plugin-sdk.md) — Building plugins
- [Plugin docs](docs/) — Individual plugin documentation

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
