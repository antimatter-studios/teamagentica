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
- [x] Kernel: Typed config_schema (string/select/number/boolean with defaults)
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
- [x] Discord plugin: capability-based AI agent discovery
- [x] Discord plugin: routes through kernel proxy to AI agents
- [x] Deleted monolithic ai-gateway (replaced by per-agent plugins)

## Phase 5: Marketplace

- [ ] Marketplace plugin protocol definition
- [ ] Default marketplace (local catalog of available plugins)
- [ ] Kernel: marketplace trust levels
- [ ] Kernel: plugin installation from marketplace catalog
- [ ] UI: marketplace browser

## Phase 6: Advanced Features

- [ ] Canary routing (v1/v2 by user identity / JWT claims)
- [ ] Named tokens (user-generated, handed to processes by reference)
- [ ] Plugin upgrade/rollback (blue-green deploy)
- [ ] Secret brokering (scoped, short-lived credentials)
- [ ] Kubernetes container runtime (alternative to Docker)
- [ ] Plugin-to-plugin gRPC protocol (protobuf schemas)
- [ ] More agent plugins: agent-anthropic, agent-ollama, agent-codex

## Phase 7: Production Readiness

- [ ] Kubernetes manifests / Helm chart
- [ ] CI/CD pipeline
- [ ] Backup/restore workflows
- [ ] Monitoring and health dashboards
- [ ] Rate limiting
- [ ] Structured JSON logging
