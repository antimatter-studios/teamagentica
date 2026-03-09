# TeamAgentica Architecture

## Overview

TeamAgentica is a modular automation control platform composed of independently deployable components that communicate over HTTP/REST with service token authentication.

## Components

### Kernel (Go Binary)

The kernel is the central authority. It is intentionally minimal.

**Responsibilities:**
- JWT-based authentication (issue, validate tokens)
- RBAC with capability-encoded JWT tokens
- Plugin registry (SQLite-backed via GORM)
- Plugin lifecycle management (install, enable, disable, restart, uninstall)
- Docker container runtime for launching plugin containers
- REST API surface for all operations
- Event hub for inter-plugin pub/sub communication
- Traffic routing proxy to plugins
- Audit logging of all actions
- Database migrations (versioned, GORM-based)
- mTLS certificate authority (optional)
- SQLite backup management

**Does NOT do:**
- AI reasoning
- Workflow execution
- UI serving
- Business logic (that's what plugins are for)

### User Interface (React/TypeScript)

A standalone web application. It is just another API client.

- Connects to kernel REST API
- Authenticates with JWT
- Renders dashboard, plugin management, chat, marketplace, cost tracking, debug console
- Completely decoupled from kernel deployment
- Could be replaced by a native app, CLI, or any HTTP client

**Key screens:**
- **Dashboard** — Plugin status overview, system health
- **Chat** — Multi-agent chat with agent/model selection
- **Plugin Settings** — Enable/disable, logs, config forms, alias management
- **Marketplace** — Browse and install plugins from providers
- **Cost Dashboard** — AI usage and cost analytics
- **Debug Console** — Real-time SSE event stream

### Plugins

Plugins are separate Docker containers that communicate with the kernel over HTTP/REST.

- Each plugin is a standalone Go binary in a Docker container
- Self-registers with kernel on startup via Plugin SDK
- Reports health via periodic heartbeats (kernel monitors with 90s timeout)
- Declares capabilities for discovery (e.g., `ai:chat`, `messaging:telegram`, `tool:image`)
- Declares config schema — kernel renders UI forms and injects config as environment variables
- Subscribes to events and receives HTTP callbacks
- Can be hot-reloaded (stopped/started without kernel restart)

**Plugin registration declares:**
- Plugin ID and version
- HTTP endpoint (auto-detected from container networking)
- Capabilities (used for discovery and routing)
- Configuration schema (drives dynamic UI forms)

### Marketplace

A plugin type that provides catalogs of available plugins.

- Default `builtin-provider` ships with the system
- Additional providers can be added via the admin UI
- All plugins are installed from a provider catalog
- Providers declare Docker image references, config schemas, and metadata
- Install flow: Browse → Install → Configure → Enable

## Communication

```
Frontend  ←→ Kernel:    HTTP/REST + JWT
Kernel    ←→ Plugins:   HTTP/REST + Service Tokens (optional mTLS)
Kernel    ←→ Database:  SQLite via GORM
Plugins   ←→ Kernel:    Event pub/sub via HTTP callbacks
External  ←→ Plugins:   Kernel webhook proxy (unauthenticated)
```

## Authentication

### Token Types

- **User tokens** — Issued on login, carry user capabilities, 24h TTL
- **Service tokens** — Pre-provisioned for automated processes (admin-created)
- **Plugin tokens** — Service tokens assigned to plugins at launch time

### Auth Flow

1. User submits credentials to `POST /api/auth/login`
2. Kernel validates against SQLite user store
3. Kernel issues JWT with encoded capabilities
4. Frontend stores JWT in localStorage, sends in `Authorization: Bearer` header
5. Kernel middleware validates JWT on every request
6. Capabilities extracted from JWT claims for RBAC decisions

### RBAC Capabilities

Each JWT contains a capabilities array. Common capabilities:
- `system:admin` — Full admin access
- `plugins:manage` — Install, configure, enable/disable plugins
- `plugins:search` — Search marketplace
- `users:read` — List users

## Event System

The kernel maintains an event hub for inter-plugin communication.

- Plugins emit events via `POST /api/plugins/event`
- Plugins subscribe to event types via `POST /api/plugins/subscribe`
- Kernel dispatches events to subscribers via HTTP callbacks
- Events can be broadcast (all subscribers) or addressed (specific plugin)
- SDK provides debouncers to prevent event floods

**Common events:**
| Event | Purpose |
|-------|---------|
| `kernel:alias:update` | Aliases changed, plugins hot-swap routing maps |
| `config:update` | Plugin config changed via admin UI |
| `usage:report` | Agent plugin reports token usage for cost tracking |
| `webhook:tunnel:update` | ngrok tunnel URL changed |
| `plugin:registered` | Plugin came online |
| `plugin:deregistered` | Plugin went offline |

## Alias Routing

Aliases map `@mention` names to specific plugins, optionally with model overrides.

```
@claude  →  agent-openai (model: gpt-4o)
@gemini  →  agent-gemini
@draw    →  tool-stability (type: image)
@video   →  tool-seedance (type: video)
```

**Routing paths in messaging bots:**

1. **Direct `@alias`** — User types `@claude explain X` → fast-path directly to the agent
2. **Coordinator delegation** — User types a generic message → coordinator agent receives `isCoordinator=true` flag → agent builds its own system prompt with available aliases → responds with `ROUTE:@alias\nmessage` to delegate → bot re-routes to target
3. **Default agent** — No aliases configured → falls back to `DEFAULT_AGENT` plugin ID

Aliases are stored in SQLite, managed via the admin UI, and hot-swapped to plugins via `kernel:alias:update` events.

### Message Buffering

Messaging plugins (Telegram, Discord) buffer sequential messages per-chat with a configurable debounce window (default 1000ms, configurable via `MESSAGE_BUFFER_MS`). This consolidates multi-part messages — such as a forwarded image followed by a text question — into a single agent request. The buffer merges text (newline-joined) and deduplicates media URLs. Commands (`/help`, `/clear`, etc.) bypass the buffer and are handled immediately.

## Plugin Lifecycle

1. **Installation** — Admin installs from marketplace or registers manually
2. **Configuration** — Admin fills config form (generated from plugin's schema)
3. **Launch** — Kernel pulls Docker image, creates container with config as env vars + service token
4. **Registration** — Plugin calls `POST /api/plugins/register` with capabilities
5. **Runtime** — Plugin sends heartbeats, kernel proxies traffic via `/api/route/:plugin_id/*`
6. **Events** — Plugin subscribes to events, receives callbacks, emits its own events
7. **Shutdown** — Admin disables or kernel stops → container receives SIGTERM → plugin deregisters

## API Routes

### Public
| Route | Purpose |
|-------|---------|
| `GET /api/health` | Health check + version |
| `POST /api/auth/login` | Login, issue JWT |
| `POST /api/auth/register` | User registration |
| `Any /api/webhook/:plugin_id/*path` | Webhook ingress (no auth) |

### User (JWT required)
| Route | Purpose |
|-------|---------|
| `GET /api/users/me` | Current user info |
| `GET /api/plugins` | List installed plugins |
| `GET /api/plugins/:id` | Plugin details |
| `GET /api/plugins/search` | Search by capability |
| `Any /api/route/:plugin_id/*path` | Proxy request to plugin |

### Admin
| Route | Purpose |
|-------|---------|
| `POST /api/plugins/:id/enable` | Enable plugin |
| `POST /api/plugins/:id/disable` | Disable plugin |
| `POST /api/plugins/:id/restart` | Restart plugin |
| `GET /api/plugins/:id/logs` | Plugin container logs |
| `GET,PUT /api/plugins/:id/config` | Plugin configuration |
| `DELETE /api/plugins/:id` | Uninstall plugin |
| `GET /api/audit` | Audit logs (paginated, filterable) |
| `POST /api/auth/service-token` | Create service token |
| `GET,POST,PUT,DELETE /api/aliases` | Manage aliases |
| `GET,POST,DELETE /api/pricing` | Manage model pricing |
| `GET,POST,PUT,DELETE /api/external-users` | External user mappings |
| `GET /api/debug/events` | SSE event stream |
| `GET /api/debug/history` | Event history |

### Plugin (service token required)
| Route | Purpose |
|-------|---------|
| `POST /api/plugins/register` | Self-registration |
| `POST /api/plugins/heartbeat` | Health heartbeat |
| `POST /api/plugins/deregister` | Graceful shutdown |
| `POST /api/plugins/event` | Emit event |
| `POST /api/plugins/subscribe` | Subscribe to event type |
| `GET /api/aliases` | Fetch alias configuration |

## Database

**Engine:** SQLite with WAL mode via GORM ORM

**Core tables:**
| Table | Purpose |
|-------|---------|
| `users` | User accounts |
| `plugins` | Plugin registry (status, config, image ref) |
| `service_tokens` | Plugin and API auth tokens |
| `audit_logs` | Action audit trail |
| `aliases` | Routing aliases (`@name` → plugin + model) |
| `external_users` | Map external IDs (Telegram/Discord) to internal users |
| `providers` | Marketplace catalog providers |
| `model_prices` | LLM pricing data with time-effective rates |
| `event_deliveries` | Event queue for async plugin notification |
| `event_logs` | Historical event audit |

**Migrations** are versioned Go functions in `kernel/internal/migrate/`, executed in order on startup.

## Deployment

### Docker Compose (recommended)

```bash
# Production
task prod:start

# Development (hot reload)
task dev:start
```

- **kernel** — Go binary, mounts Docker socket for plugin orchestration
- **user-interface** — React build served by nginx

### Local Development

```bash
# Kernel with hot reload
cd kernel && air

# Frontend with HMR
cd user-interface && npm run dev
```

### Plugin Images

Each plugin has a Dockerfile. Build all images with:

```bash
task build:images
```

The kernel launches plugin containers on-demand via the Docker API.

## Security

| Layer | Mechanism |
|-------|-----------|
| User auth | JWT with 24h TTL, capability claims |
| Plugin auth | Service tokens (scoped, revokable) |
| Transport | Optional mTLS between kernel and plugins |
| Authorization | Capability-based RBAC on every endpoint |
| Audit | Every action logged (actor, resource, IP, timestamp) |
| Isolation | Each plugin in its own Docker container + volume |
| Secrets | Plugin configs marked `secret: true` stored encrypted-at-rest |

## Plugin SDK

The `pkg/pluginsdk` Go package provides:

- `NewClient()` — Create SDK client with registration info
- `Start(ctx)` — Register with kernel, start heartbeat loop
- `Stop()` — Deregister gracefully
- `OnEvent(type, handler)` — Subscribe to events with optional debouncing
- `ReportEvent(type, detail)` — Broadcast event
- `ReportAddressedEvent(type, detail, target)` — Send to specific plugin
- `FetchAliases()` — Get current alias configuration
- `alias.AliasMap` — Thread-safe alias routing with hot-swap via `atomic.Pointer`
- `ConfigSchemaField` — Declare config UI (string, select, number, boolean, text, oauth, aliases)
