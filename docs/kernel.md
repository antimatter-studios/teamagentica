# Kernel

> The minimal, stable control plane that orchestrates plugins and enforces security boundaries.

## Overview

The kernel is the central governance layer of TeamAgentica. It handles authentication, authorization, plugin lifecycle management, event routing, and API proxying. It is intentionally small and boring — all capabilities come from plugins. The kernel never executes AI inference or business logic directly.

Built with Go/Gin, SQLite (GORM), and the Docker API.

## What the Kernel Does

- Authenticates users (JWT) and plugins (service tokens)
- Enforces RBAC with capability-based access control
- Manages plugin containers via Docker API
- Routes HTTP requests between frontend, plugins, and external webhooks
- Runs a pub/sub event system for inter-plugin communication
- Stores configuration, audit logs, pricing, and aliases
- Provides a marketplace for plugin discovery and installation
- Monitors plugin health and auto-restarts unhealthy containers
- Manages mTLS certificates for secure plugin communication
- Backs up the SQLite database on a configurable interval

## What the Kernel Does NOT Do

- Execute AI inference
- Process chat messages
- Store conversation history
- Generate images or video
- Connect to messaging platforms

## API Reference

### Public (no authentication)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/health` | Health check — returns `{status, version, app}` |
| `POST` | `/api/auth/register` | Register user (first user becomes admin; subsequent require admin JWT) |
| `POST` | `/api/auth/login` | Login — returns JWT + user object |
| `ANY` | `/api/webhook/:plugin_id/*path` | Webhook ingress proxy — forwards to plugin without auth |

### User (valid JWT required)

| Method | Path | Capability | Description |
|--------|------|------------|-------------|
| `GET` | `/api/users/me` | any | Current user info |
| `GET` | `/api/users` | `users:read` | List all users |
| `GET` | `/api/plugins/search` | `plugins:search` | Search running plugins by capability prefix |
| `POST` | `/api/plugins` | `plugins:manage` | Register a plugin |
| `GET` | `/api/plugins` | `plugins:manage` | List all plugins |
| `GET` | `/api/plugins/:id` | `plugins:manage` | Get plugin details |
| `DELETE` | `/api/plugins/:id` | `plugins:manage` | Uninstall plugin (blocked for system plugins) |
| `POST` | `/api/plugins/:id/enable` | `plugins:manage` | Pull image + start container |
| `POST` | `/api/plugins/:id/disable` | `plugins:manage` | Stop container (blocked for system plugins) |
| `POST` | `/api/plugins/:id/restart` | `plugins:manage` | Restart container |
| `GET` | `/api/plugins/:id/logs` | `plugins:manage` | Container logs (`?tail=N`, default 100) |
| `GET` | `/api/plugins/:id/config` | `plugins:manage` | Get config (secrets masked) |
| `PUT` | `/api/plugins/:id/config` | `plugins:manage` | Update config (`?soft=true` for hot reload) |
| `GET` | `/api/marketplace/providers` | `plugins:manage` | List marketplace providers |
| `POST` | `/api/marketplace/providers` | `plugins:manage` | Add marketplace provider |
| `DELETE` | `/api/marketplace/providers/:id` | `plugins:manage` | Delete provider (blocked for system) |
| `GET` | `/api/marketplace/plugins` | `plugins:manage` | Browse plugins (`?q=search`) |
| `POST` | `/api/marketplace/install` | `plugins:manage` | Install plugin from marketplace |
| `ANY` | `/api/route/:plugin_id/*path` | any | Reverse proxy to plugin |

### Admin (`system:admin` capability)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/auth/service-token` | Create service token |
| `GET` | `/api/auth/service-tokens` | List service tokens |
| `DELETE` | `/api/auth/service-token/:id` | Revoke service token |
| `GET` | `/api/audit` | Query audit logs (`?action=&actor_id=&limit=&offset=`) |
| `GET` | `/api/pricing` | All pricing history |
| `GET` | `/api/pricing/current` | Current active prices |
| `POST` | `/api/pricing` | Set price (closes old window) |
| `DELETE` | `/api/pricing/:id` | Delete price entry |
| `GET` | `/api/external-users` | List external user mappings (`?source=`) |
| `POST` | `/api/external-users` | Create external user mapping |
| `PUT` | `/api/external-users/:id` | Update external user mapping |
| `DELETE` | `/api/external-users/:id` | Delete external user mapping |
| `POST` | `/api/aliases` | Create/update alias |
| `PUT` | `/api/aliases` | Bulk replace admin-owned aliases |
| `DELETE` | `/api/aliases/:name` | Delete alias |
| `GET` | `/api/debug/events` | SSE event stream (keepalive every 15s) |
| `GET` | `/api/debug/history` | In-memory event history (`?limit=N`, default 200) |
| `GET` | `/api/debug/event-log` | Persistent event log (`?limit=N`, default 100) |
| `GET` | `/api/debug/test` | Emit test event |

### Plugin (service token auth)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/aliases` | List aliases (enriched with target capabilities) |
| `POST` | `/api/plugins/register` | Self-register host/port after startup |
| `POST` | `/api/plugins/heartbeat` | Heartbeat (every 30s from SDK) |
| `POST` | `/api/plugins/deregister` | Deregister and remove subscriptions |
| `POST` | `/api/plugins/event` | Emit event (broadcast or addressed) |
| `POST` | `/api/plugins/subscribe` | Subscribe to event type |
| `POST` | `/api/plugins/unsubscribe` | Unsubscribe from event type |
| `POST` | `/api/plugins/pricing` | Push model pricing data |

## Authentication & RBAC

### JWT Tokens

- Algorithm: HMAC-SHA256
- User token expiry: 24 hours
- Service token expiry: 365 days (configurable)
- Claims: `user_id`, `email`, `role`, `capabilities`

### Roles

| Role | Capabilities |
|------|-------------|
| `admin` | `users:read`, `users:write`, `plugins:manage`, `plugins:search`, `system:admin` |
| `user` | `users:read:self`, `plugins:search` |
| `service` | Custom (set at creation, whitelist: `plugins:search`, `plugins:manage`, `users:read`, `system:admin`) |

### First-User Bootstrap

The first `POST /api/auth/register` call succeeds without authentication and creates an admin user. All subsequent registrations require an admin JWT.

### Password Hashing

bcrypt with cost factor 12.

## Database Schema

SQLite with WAL mode, busy timeout 5000ms, synchronous=NORMAL.

### Tables

| Table | Purpose |
|-------|---------|
| `users` | User accounts (email, password_hash, role) |
| `plugins` | Plugin registry (id, image, status, capabilities, config_schema) |
| `plugin_configs` | Key-value config per plugin (supports secrets) |
| `service_tokens` | Service token metadata (token_hash for revocation) |
| `audit_logs` | Immutable action log (actor, action, resource, IP) |
| `providers` | Marketplace provider URLs |
| `model_prices` | Time-windowed pricing (input/output/cached per 1M tokens) |
| `event_subscriptions` | Plugin event subscriptions (plugin_id, event_type, callback_path) |
| `events` | Pending addressed event delivery queue (max 1000 per target) |
| `aliases` | `@mention` routing aliases (admin-owned or plugin-owned) |
| `external_users` | External platform user mappings (Telegram ID, Discord ID, etc.) |
| `event_log` | Persistent inter-plugin event history |

### Database Resilience

- **Backups**: SQLite native Backup API, 3 rotating slots, configurable interval (default 5m)
- **Corruption watchdog**: Detects `SQLITE_CORRUPT`/`SQLITE_NOTADB`, attempts `REINDEX` + integrity check, auto-restores from newest valid backup

## Event System

### Debug Events (SSE)

The kernel maintains a 500-event ring buffer broadcast over SSE at `/api/debug/events`. Event types include: `proxy`, `register`, `deregister`, `heartbeat`, `subscribe`, `dispatch`, `config:update`, `alias:upsert`, `error`, `warning`, etc.

### Inter-Plugin Events

Plugins subscribe to event types and receive HTTP POST callbacks.

**Broadcast (fire-and-forget)**: Delivered to all subscribers. No persistence, no retry.

**Addressed (at-least-once)**: Persisted to `events` table. Delivered immediately if target is online, queued otherwise. Flushed when target registers or subscribes. Per-target cap: 1000 events (oldest evicted).

**Payload format**:
```json
{
  "event_type": "usage:report",
  "plugin_id": "agent-openai",
  "detail": "{...}",
  "timestamp": "2025-01-01T00:00:00Z"
}
```

**Kernel-emitted events**:
- `plugin:registered` — broadcast when a plugin self-registers
- `kernel:alias:update` — broadcast (debounced 2s) on alias changes, includes monotonic sequence number
- `config:update` — addressed to specific plugin on soft config update

## Plugin Lifecycle

### Startup Sequence

1. Kernel queries DB for `enabled=true` plugins
2. For each: verifies service token, builds env from config + schema defaults
3. Injects kernel address, plugin ID, plugin token, and mTLS certs
4. Pulls Docker image, creates and starts container
5. Plugin SDK calls `POST /api/plugins/register` with host/port/capabilities
6. Kernel updates status, fires `plugin:registered`, flushes pending events

### Container Configuration

- Naming: `teamagentica-plugin-<id>`
- Data volume: `teamagentica-data-<id>` mounted at `/data`
- Network: configurable Docker bridge (default `teamagentica`)
- Restart policy: `unless-stopped`
- Dev mode: bind-mounts plugin source + SDK + shared Go cache volumes

### Health Monitoring

- Check interval: 30 seconds
- Heartbeat staleness: 90 seconds
- Auto-restart threshold: 4 consecutive failures (~2 minutes)
- Checks: heartbeat freshness → Docker container health → auto-restart

### System Plugins

| Plugin | Capabilities | Notes |
|--------|-------------|-------|
| `builtin-provider` | `marketplace:provider` | Cannot be disabled or uninstalled |
| `infra-cost-explorer` | `system:cost-explorer` | Cannot be disabled or uninstalled |

## mTLS

When `TEAMAGENTICA_MTLS_ENABLED=true` (default):

- Self-signed CA generated once (ECDSA P256, 10-year validity)
- Kernel cert with SANs: `kernel`, `localhost`, `teamagentica-kernel`, `127.0.0.1`
- Per-plugin cert with SANs: `<plugin_id>`, `teamagentica-plugin-<plugin_id>`, `localhost`, `127.0.0.1`
- Server mode: `VerifyClientCertIfGiven` (health checks work without certs)
- Certs stored at `$DATA_DIR/certs/`

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TEAMAGENTICA_JWT_SECRET` | **required** | JWT signing secret |
| `APP_NAME` | `TeamAgentica` | Brand name |
| `TEAMAGENTICA_KERNEL_HOST` | `0.0.0.0` | Bind host |
| `TEAMAGENTICA_KERNEL_PORT` | `8080` | Bind port |
| `TEAMAGENTICA_KERNEL_ADVERTISE_HOST` | same as host | Address plugins use to reach kernel |
| `TEAMAGENTICA_DB_PATH` | `./database.db` | SQLite path |
| `TEAMAGENTICA_DATA_DIR` | `./data` | Data directory |
| `TEAMAGENTICA_DOCKER_NETWORK` | `teamagentica` | Docker network |
| `TEAMAGENTICA_RUNTIME` | `docker` | Container runtime |
| `TEAMAGENTICA_MTLS_ENABLED` | `true` | Enable mTLS |
| `TEAMAGENTICA_BACKUP_INTERVAL` | `5m` | SQLite backup interval |
| `TEAMAGENTICA_PROVIDER_URL` | `""` | Default marketplace provider URL |
| `TEAMAGENTICA_DEV_MODE` | `false` | Dev mode (`:dev` tags, source mounts) |
| `TEAMAGENTICA_PROJECT_ROOT` | `""` | Monorepo path for dev bind mounts |

## Related

- [Plugin SDK](plugin-sdk.md) — How to build plugins
- [Architecture](architecture.md) — System architecture overview
