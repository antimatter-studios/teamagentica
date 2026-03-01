# Roboslop — Build Todo

## Phase 1: Foundation — COMPLETE

- [x] Project structure and documentation
- [x] Kernel: Go project setup (go.mod, main.go)
- [x] Kernel: SQLite + GORM setup with migrations
- [x] Kernel: User model (email, hashed password, role)
- [x] Kernel: JWT auth (login, token issue, token validation)
- [x] Kernel: RBAC middleware
- [x] Kernel: REST API endpoints (login, register, me, users)
- [x] Kernel: Dockerfile
- [x] Kernel: Dev tooling (Air hot reload + Taskfile.yml)
- [x] User Interface: React/TS project setup (Vite)
- [x] User Interface: Login form with technical aesthetic
- [x] User Interface: Dev/prod modes (Vite HMR + nginx)
- [x] User Interface: API client connecting to ROBOSLOP_KERNEL_HOST/PORT
- [x] User Interface: Dockerfile

## Phase 1.5: AI Agent + Discord — COMPLETE

- [x] AI Gateway: Extracted from kernel into plugins/ai-gateway/
- [x] AI Gateway: Agent config model + CRUD endpoints
- [x] AI Gateway: Chat endpoint (OpenAI-compatible)
- [x] AI Gateway: Auth delegation to kernel
- [x] AI Gateway: Own SQLite for config storage
- [x] Discord plugin: Bot connects to Discord
- [x] Discord plugin: Forward messages to kernel AI chat
- [x] Discord plugin: Send AI responses back to channel
- [x] Discord plugin: Dev tooling (Air + Taskfile)

## Phase 2: Plugin System — COMPLETE

- [x] Kernel: Plugin registry (SQLite model)
- [x] Kernel: Plugin config storage (per-plugin key-value with secrets)
- [x] Kernel: Docker container runtime (Docker SDK)
- [x] Kernel: Plugin lifecycle (install, enable, disable, restart, uninstall)
- [x] Kernel: Capability-based plugin discovery API
- [x] Kernel: Health monitoring goroutine
- [x] Kernel: Plugin container log access
- [x] UI: Plugin management page (list, install, config, logs)
- [x] Docker Compose: all services on shared network
- [ ] Define protobuf schemas for plugin-to-plugin gRPC protocol
- [ ] Plugin-to-plugin routing via kernel

## Phase 3: Marketplace

- [ ] Marketplace plugin protocol definition
- [ ] Default marketplace (local catalog)
- [ ] Kernel: marketplace trust levels
- [ ] Kernel: plugin installation from marketplace
- [ ] UI: marketplace browser

## Phase 4: Advanced Features

- [ ] Canary routing (v1/v2 by user identity)
- [ ] Audit logging
- [ ] Service tokens and named tokens
- [ ] Plugin upgrade/rollback
- [ ] Secret brokering (scoped credentials)

## Phase 5: AI Agent Integration

- [ ] Agent connector plugin
- [ ] Scoped short-lived token issuance
- [ ] Approval workflow for agent-requested actions
- [ ] Policy enforcement for agent operations

## Phase 6: Production Readiness

- [ ] Kubernetes manifests / Helm chart
- [ ] CI/CD pipeline
- [ ] Backup/restore workflows
- [ ] Monitoring and health dashboards
