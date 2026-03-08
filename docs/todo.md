# TeamAgentica — Build Todo

## Phase 1: Foundation — COMPLETE

- [x] Project structure and documentation
- [x] Kernel: Go project setup, SQLite/GORM, user model, JWT auth, RBAC middleware
- [x] Kernel: REST API endpoints (login, register, me, users)
- [x] Kernel: Dev tooling (Air hot reload + Taskfile.yml)
- [x] Kernel: Dockerfile
- [x] User Interface: React/TS Vite app, cyberpunk theme, login/dashboard
- [x] User Interface: Dev/prod modes (Vite HMR + nginx)
- [x] Docker Compose: kernel + UI only (kernel manages everything else)

## Phase 2: Plugin System — COMPLETE

- [x] Plugin SDK: shared Go module (register/heartbeat/deregister)
- [x] Kernel: Plugin registry (SQLite model, CRUD endpoints)
- [x] Kernel: Plugin config storage (per-plugin key-value with secrets)
- [x] Kernel: Typed config_schema (string/select/number/boolean/text/oauth/aliases)
- [x] Kernel: Docker container runtime (Docker SDK)
- [x] Kernel: Plugin lifecycle (install, enable, disable, restart, uninstall)
- [x] Kernel: Capability-based plugin discovery API
- [x] Kernel: Health monitoring (heartbeat + Docker inspect, 90s timeout)
- [x] Kernel: Traffic routing proxy (/api/route/:plugin_id/*)
- [x] Kernel: Boot orchestration (auto-start enabled plugins)
- [x] Kernel: Graceful shutdown (stop all plugins)
- [x] UI: Plugin management page (list, install, config, logs)

## Phase 3: Security — COMPLETE

- [x] mTLS: kernel as CA, auto-generated ECDSA P-256 certs per plugin
- [x] mTLS: optional via TEAMAGENTICA_MTLS_ENABLED (default true)
- [x] Service tokens: admin endpoint for scoped plugin JWTs
- [x] Service token revocation
- [x] Audit logging: all auth/plugin actions to SQLite
- [x] Audit log query endpoint (paginated, filterable)

## Phase 4: AI + Messaging — COMPLETE

- [x] agent-openai plugin: /health + /chat (env-based config)
- [x] agent-gemini plugin: Google Gemini support
- [x] agent-kimi plugin: Moonshot Kimi support
- [x] agent-openrouter plugin: OpenRouter multi-model support
- [x] agent-requesty plugin: Requesty API support
- [x] Discord plugin: capability-based AI agent discovery, alias routing
- [x] Telegram plugin: polling/webhook modes, alias routing, coordinator delegation
- [x] WhatsApp plugin: WhatsApp Business API integration
- [x] Deleted monolithic ai-gateway (replaced by per-agent plugins)

## Phase 5: Marketplace — COMPLETE

- [x] Marketplace plugin protocol (provider catalogs via REST)
- [x] builtin-provider plugin (default catalog of all available plugins)
- [x] Kernel: marketplace provider management (add/remove providers)
- [x] Kernel: plugin installation from marketplace catalog
- [x] UI: marketplace browser (browse, search, install from providers)

## Phase 6: Platform Features — COMPLETE

- [x] Event system: pub/sub hub, plugin subscriptions, HTTP callbacks
- [x] Event system: broadcast + addressed events, debouncing
- [x] Alias routing: @mention aliases, coordinator delegation protocol
- [x] Alias hot-swap via kernel:alias:update events
- [x] Chat plugin: conversation orchestration, history, agent routing
- [x] Cost-explorer plugin: usage tracking, per-model cost analytics
- [x] Pricing management: time-effective model pricing
- [x] Tool plugins: stability, seedance, nanobanana, veo (image/video generation)
- [x] Webhook ingress: external webhook routing to plugins
- [x] ngrok plugin: public tunnel URL generation
- [x] MCP server plugin: Model Context Protocol support
- [x] storage-sss3 plugin: S3-compatible file storage
- [x] Scheduler plugin: cron-style task scheduling
- [x] External user mapping (Telegram/Discord IDs → internal users)
- [x] SSE debug console: real-time event stream in UI
- [x] SQLite database backups
- [x] Database corruption watchdog

## Phase 7: Production Readiness

- [ ] Kubernetes manifests / Helm chart
- [ ] CI/CD pipeline
- [ ] Monitoring and health dashboards
- [ ] Rate limiting
- [ ] Structured JSON logging
- [ ] Canary routing (v1/v2 by user identity / JWT claims)
- [ ] Named tokens (user-generated, handed to processes by reference)
- [ ] Plugin upgrade/rollback (blue-green deploy)
- [ ] Secret brokering (scoped, short-lived credentials)
- [ ] Plugin-to-plugin direct communication protocol
