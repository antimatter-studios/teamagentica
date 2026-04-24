# TeamAgentica vs NanoClaw: Architecture Comparison

A deep technical comparison between **TeamAgentica** (this project) and **[NanoClaw](https://github.com/qwibitai/nanoclaw)**, two systems that run AI agents in isolated containers with multi-channel messaging support.

---

## Executive Summary

Both projects solve the same core problem — securely running AI agents in containers with messaging platform integration — but from opposite ends of the design spectrum.

| Dimension | TeamAgentica | NanoClaw |
|-----------|----------|----------|
| **Philosophy** | Governance-first platform for teams | Personal assistant for a single user |
| **Architecture** | Microkernel + Docker plugin ecosystem | Monolithic Node.js process |
| **Language** | Go (kernel) + TypeScript (UI) | TypeScript (everything) |
| **Scale target** | Multi-user, multi-node k3s cluster | Single machine, single user |
| **Plugin model** | Docker containers with SDK registration | Source code transformation via "skills" |
| **Inter-process comm** | HTTP/REST + event pub/sub | Filesystem-based IPC (JSON files) |
| **Agent runtime** | Any (plugins bring their own AI SDK) | Claude Agent SDK exclusively |
| **Security model** | mTLS + JWT + RBAC + audit logging | Container filesystem isolation + mount allowlists |
| **Data storage** | Kernel SQLite + per-plugin Docker volumes | Single SQLite database |
| **Deployment** | Docker Compose (dev), k3s + Gitea CI (prod) | launchd/systemd single process |
| **Codebase size** | ~15K LOC across kernel + plugins + UI | ~3K LOC (by design) |

---

## Architecture Deep Dive

### TeamAgentica: Microkernel + Plugin Ecosystem

```
User ──► React UI ──JWT──► Go Kernel ──mTLS──► Plugin Containers
                              │                     │
                           SQLite/GORM          Docker volumes
                              │                     │
                         (users, plugins,      (per-plugin
                          pricing, audit)       isolated data)
```

- **Kernel** is a central authority (~1,900 LOC Go/Gin) that manages plugin lifecycle, auth, routing, events, and audit
- **Plugins** are independent Docker containers that self-register via HTTP, communicate through the kernel's routing proxy, and emit/subscribe to events
- **SDK** (`pluginsdk` Go package) handles registration, heartbeat, event reporting, mTLS setup
- **Frontend** is a full React SPA with marketplace, plugin management, cost dashboard, debug console

### NanoClaw: Monolithic Orchestrator

```
User ──► WhatsApp/Telegram ──► Node.js Process ──► Docker Container
                                     │                    │
                                  SQLite              Claude Agent SDK
                                     │                    │
                              (messages, groups,     (filesystem IPC,
                               tasks, sessions)       MCP tools)
```

- **Single process** handles message routing, scheduling, IPC, and container lifecycle
- **Channels** self-register at import time via a factory registry
- **Containers** are ephemeral (fresh per invocation, `--rm`), spawned via `docker run` CLI
- **IPC** is entirely filesystem-based: host writes JSON files into mounted dirs, container polls them
- No web UI, no REST API, no admin dashboard — managed through the messaging platform itself

---

## What NanoClaw Does Better

### 1. Container Reuse via Query Loop

**NanoClaw** keeps containers alive between messages. After completing a query, the agent-runner enters an idle loop, polling for new IPC messages. Follow-up messages are piped into the running container rather than spawning a new one. An idle timeout (`IDLE_TIMEOUT`, default 30min) eventually shuts it down.

**TeamAgentica** plugins are long-lived containers that stay running permanently. This is simpler but uses resources even when idle. NanoClaw's approach is more resource-efficient for bursty personal use.

**Potential benefit**: For plugins with expensive startup (loading models, establishing connections), a hybrid approach — keep containers running but with auto-sleep/wake — could reduce resource usage on the cluster.

### 2. Conversation Context Accumulation

**NanoClaw** accumulates all messages in a group between agent invocations. When a trigger message arrives, the agent receives the *entire conversation* since its last response, not just the trigger message. This two-cursor system (`lastTimestamp` for "seen" vs `lastAgentTimestamp` for "processed") gives agents full context.

**TeamAgentica's** Telegram bot only sends the triggering message plus maintains an in-memory conversation history (max 20 pairs). It doesn't capture messages between bot interactions from other group members.

**Potential benefit**: Adopting conversation accumulation in our Telegram/WhatsApp bots would give agents much richer context in group chats, especially when users discuss something and then ask the bot to weigh in.

### 3. Scheduled Task System

**NanoClaw** has a built-in scheduler with cron, interval, and one-time tasks. Tasks run as full agent invocations in isolated containers with access to all tools. An MCP server inside the container exposes scheduling tools to the agent itself, so users can say "remind me every Monday at 9am" and the agent creates the task.

**TeamAgentica** has no task scheduling. Agents respond to messages but cannot autonomously perform actions on a schedule.

**Potential benefit**: A `task-scheduler` plugin could provide similar functionality. The kernel's event system could trigger scheduled plugin invocations. This would enable proactive agents (daily summaries, monitoring alerts, periodic reports).

### 4. Per-Group Memory via CLAUDE.md Hierarchy

**NanoClaw** has a clean hierarchical memory system:
- `groups/CLAUDE.md` — global context (all groups read, only main writes)
- `groups/{name}/CLAUDE.md` — per-group memory
- Additional `.md` files per group for notes, research, etc.

The Claude Agent SDK automatically loads these via `settingSources: ['project']` and `additionalDirectories`. Memory persists across container restarts.

**TeamAgentica** relies on in-memory conversation history per chat (lost on restart) and whatever the AI agent plugin internally maintains.

**Potential benefit**: Per-chat persistent context files, either as mounted volumes or via a memory plugin, would significantly improve long-term conversation quality.

### 5. Skills-Based Extension Model

**NanoClaw** uses a deterministic code transformation engine for extensions. A "skill" is a Claude Code slash command that physically modifies the codebase — adds files, merges changes, installs dependencies. The engine handles three-way merges, rollback on test failure, conflict markers, and state tracking.

This is a novel approach: instead of a plugin API, you fork the project and apply transformations. Each installation becomes a bespoke codebase.

**TeamAgentica** uses a traditional plugin model with SDK, Docker images, and a marketplace. This scales better for teams but requires more infrastructure.

**Potential benefit**: The skills concept could complement our plugin system — a skill could scaffold a new plugin project with boilerplate, Dockerfile, CI pipeline, and SDK integration. `/create-plugin messaging-telegram` could generate a ready-to-deploy plugin.

### 6. Apple Container Support

**NanoClaw** supports both Docker and Apple's native container runtime (`container` CLI), with automatic adaptation of mount syntax, startup checks, and privilege handling. Apple Containers are lightweight Linux VMs on macOS with better integration than Docker Desktop.

**TeamAgentica** is Docker-only.

**Potential benefit**: Low priority for our k3s production deployment, but useful for local development on macOS without Docker Desktop.

### 7. Filesystem-Based IPC (Simplicity)

**NanoClaw's** IPC is just JSON files in mounted directories. No sockets, no HTTP, no gRPC. The host writes a file, the container polls for it. Atomic writes via temp-file-then-rename prevent race conditions. Failed files move to `errors/` for debugging.

This is remarkably simple and debuggable — you can literally `ls` the IPC directory to see pending messages.

**TeamAgentica** uses HTTP/REST with mTLS between kernel and plugins, which is more robust but harder to debug.

**Potential benefit**: Not directly applicable (our HTTP approach is better for a multi-node cluster), but the principle of debuggability is worth noting. Our debug console SSE stream serves a similar purpose.

### 8. Session Continuity via Claude Agent SDK

**NanoClaw** uses the Claude Agent SDK's `resume` and `resumeSessionAt` features to maintain conversation context across container restarts. Session transcripts are stored as JSONL files, and a `PreCompact` hook archives conversations to markdown before session compaction.

**TeamAgentica** doesn't use Claude Agent SDK directly — it's agent-agnostic, supporting OpenAI, Gemini, Kimi, etc. Each agent plugin manages its own context.

**Potential benefit**: Not directly applicable since we support multiple AI providers, but plugins could implement similar session persistence patterns.

---

## What TeamAgentica Does Better

### 1. Multi-Provider AI Support

**TeamAgentica** supports any AI provider as a plugin: OpenAI, Gemini, Kimi, OpenRouter, Requesty, and more. Users can switch models per-chat via `/model` commands. The capability-based discovery system (`agent:chat`) means the Telegram bot automatically finds whichever AI agent is running.

**NanoClaw** is Claude-only. It uses the Claude Agent SDK exclusively and requires a Claude Code subscription or Anthropic API key.

**Technical advantage**: Our kernel's routing proxy (`/api/route/{plugin_id}/*path`) lets any frontend or messaging bot talk to any AI agent without knowing implementation details. Adding a new AI provider is just deploying a new plugin — no changes to the kernel, bots, or UI.

### 2. Web UI with Full Management

**TeamAgentica** has a React SPA with:
- **Marketplace**: Browse, search, install plugins from catalog providers
- **Plugin management**: Enable/disable/restart, view logs, edit configuration
- **Cost dashboard**: Per-model cost tracking with hourly/daily/weekly/monthly granularity
- **Debug console**: Real-time SSE event stream for troubleshooting
- **Pricing editor**: Configure per-model token costs

**NanoClaw** has no web interface. All management happens through the messaging platform ("@Andy add group X", "@Andy list groups").

**Technical advantage**: A visual management interface is essential for operating a multi-plugin system. Real-time SSE debug events, configuration forms generated from `config_schema`, and centralized log viewing are capabilities that CLI/chat-based management cannot match.

### 3. Production-Grade Security

**TeamAgentica's** security model is comprehensive:
- **mTLS**: Mutual TLS between kernel and plugins with auto-generated CA, per-plugin certs, and cert rotation
- **JWT auth**: User tokens (24h TTL) with role-based claims
- **RBAC**: Admin vs user roles with fine-grained capabilities (`users:read`, `plugins:manage`, `system:admin`)
- **Service tokens**: Per-plugin authentication with independent scoping
- **Audit logging**: Every action logged with actor, resource, timestamp, IP, success/failure
- **Plugin isolation**: Each plugin gets its own Docker network, volume, and service token

**NanoClaw's** security relies primarily on container filesystem isolation and a mount allowlist. There's no authentication between host and container, no audit trail, no multi-user access control. The security doc acknowledges that "the agent itself can discover [auth] credentials via Bash."

**Technical advantage**: Our mTLS ensures plugins can't impersonate each other or the kernel. Audit logging provides forensic capability. RBAC enables team use. These are non-negotiable for any production deployment.

### 4. Inter-Plugin Communication

**TeamAgentica** has a proper event system:
- Plugins emit events via `POST /api/plugins/event`
- Other plugins subscribe to event types and receive HTTP callbacks
- The routing proxy enables direct plugin-to-plugin communication through the kernel
- Example: network-webhook-ingress emits `webhook:url` → Telegram plugin receives tunnel URL

**NanoClaw** has no inter-process communication beyond host↔container IPC. Containers are isolated from each other by design.

**Technical advantage**: Event-driven architecture enables loose coupling and emergent behavior. The infra-cost-explorer subscribing to `usage:report` events from all plugins is a pattern that cannot exist in NanoClaw's model.

### 5. Plugin Marketplace and Distribution

**TeamAgentica** has a complete plugin distribution system:
- **Catalog providers**: REST endpoints serving plugin catalogs with metadata, config schemas, and Docker image references
- **Built-in provider**: System plugin serving the core plugin catalog
- **Install flow**: Browse → Install → Configure → Enable, all from the UI
- **Config schemas**: JSON schemas that generate configuration forms in the UI
- **Docker registry**: Private registry for plugin images

**NanoClaw** distributes extensions as "skills" (Claude Code slash commands that modify source). This requires forking the repo and running transformations — there's no package manager or catalog.

**Technical advantage**: Our marketplace model scales to many plugins and many users. A team can install and configure plugins without touching code. Version management, updates, and rollbacks are all handled through Docker image tags.

### 6. Cost Tracking and Usage Analytics

**TeamAgentica** tracks AI usage across all providers:
- Each plugin reports usage via `sdk.ReportUsage()` events
- Cost-explorer plugin aggregates records with SQLite
- UI shows costs per hour/day/week/month with per-model breakdown
- Pricing table supports time-effective pricing (rate changes over time)
- Token-level detail: input, output, cached, reasoning tokens + duration

**NanoClaw** has no usage tracking or cost visibility.

**Technical advantage**: Essential for controlling AI spend across multiple providers. The pricing table with `effective_from`/`effective_to` dates handles provider price changes correctly.

### 7. Tool Plugin Ecosystem

**TeamAgentica** supports tool plugins beyond AI chat:
- **Video generation**: Veo, Seedance, Stability, NanoBanana — discoverable via `agent:tool:video` capability
- **Image generation**: via `agent:tool:image` capability
- **Cost tracking**: `system:cost-explorer`
- **Tunnel/webhooks**: network-traffic-manager, network-webhook-ingress

The capability-based discovery system means messaging bots auto-discover available tools. The Telegram bot dynamically creates `/<provider>` commands for each running video tool.

**NanoClaw** has browser automation (agent-browser + Chromium) and web tools built into the container, but no concept of tool plugins.

**Technical advantage**: The plugin model allows adding capabilities without modifying any existing code. Deploying a new video tool instantly makes it available to all messaging bots.

### 8. Multi-Node Deployment

**TeamAgentica** deploys to a 4-node k3s cluster with:
- Longhorn distributed storage (3 replicas)
- Traefik ingress with Let's Encrypt TLS
- Gitea Actions CI/CD pipeline
- Pod security (restricted, runAsNonRoot, seccomp)
- RBAC service accounts for deployment

**NanoClaw** runs on a single machine as a launchd/systemd service.

**Technical advantage**: Horizontal scaling, high availability, automated deployment, and infrastructure-as-code are production requirements that NanoClaw explicitly does not target.

---

## Features Worth Importing

### High Value

| Feature | NanoClaw Implementation | Suggested TeamAgentica Approach | Effort |
|---------|------------------------|-----------------------------|--------|
| **Scheduled tasks** | Host scheduler + container invocation + MCP tools | New `task-scheduler` system plugin. Store cron/interval/once tasks in SQLite. On trigger, call target plugin via kernel route proxy. Expose to messaging bots as `/schedule` command. | Medium |
| **Conversation accumulation** | Two-cursor SQLite polling (seen vs processed) | Add to Telegram/WhatsApp bots: store all group messages in local SQLite, include full context since last bot response when triggered. | Low |
| **Per-chat persistent memory** | CLAUDE.md files in per-group dirs | New `memory` plugin or extend messaging bots to store per-chat context in their Docker volume. Persist across restarts. | Low |

### Medium Value

| Feature | NanoClaw Implementation | Suggested TeamAgentica Approach | Effort |
|---------|------------------------|-----------------------------|--------|
| **Plugin scaffolding CLI** | Skills engine (code transformation) | `teamagentica create-plugin <name>` CLI tool that generates boilerplate: main.go, Dockerfile, .air.toml, config, handlers, CI pipeline. | Low |
| **Conversation archival** | PreCompact hook → markdown files | Messaging plugins could periodically export conversation history to persistent storage or a dedicated archive plugin. | Low |
| **Container auto-sleep** | Idle timeout → `_close` sentinel | For expensive plugins that aren't always needed: kernel health monitor could stop idle plugin containers and restart on first request. | Medium |

### Low Value (For Our Use Case)

| Feature | Why Low Priority |
|---------|-----------------|
| Apple Container support | We deploy to Linux k3s, not macOS |
| Filesystem IPC | HTTP/mTLS is better for multi-node |
| Claude Agent SDK resume | We're multi-provider, not Claude-exclusive |
| Skills engine | Our marketplace/Docker model is more scalable |

---

## Architectural Philosophy Comparison

### NanoClaw: "Small and Comprehensible"

> "The entire codebase should be readable and understandable, avoiding microservices, message queues, and unnecessary abstraction layers."

NanoClaw explicitly rejects the complexity of systems like OpenClaw ("4-5 different processes, endless configuration"). It optimizes for a single developer who can read and understand every line. The skills model means each installation is a custom fork — no two NanoClaw instances are identical.

**Strengths**: Fast to understand, easy to debug, minimal dependencies (6 runtime deps), no configuration sprawl.

**Weaknesses**: Single-user only, no horizontal scaling, Claude-exclusive, no management UI, no multi-tenancy, security relies on container isolation alone.

### TeamAgentica: "Governance-First Platform"

TeamAgentica optimizes for operational control: who did what, when, how much did it cost, and can we audit it? The microkernel + plugin model allows independent deployment and versioning of components. mTLS, RBAC, and audit logging are first-class concerns.

**Strengths**: Multi-provider, multi-user, production-ready security, visual management, cost tracking, extensible marketplace, CI/CD pipeline.

**Weaknesses**: More complex to understand, more infrastructure to operate, heavier resource footprint, requires Docker registry and k3s cluster for production.

---

## Summary

NanoClaw and TeamAgentica represent two valid but different approaches to the same problem:

- **NanoClaw** is a personal tool: lean, single-user, Claude-only, managed through chat. It excels at simplicity and developer comprehension. Best for an individual running a personal AI assistant on their Mac.

- **TeamAgentica** is a platform: governed, multi-provider, multi-user, managed through a web UI. It excels at operational control and extensibility. Best for a team or individual who needs multiple AI providers, cost visibility, and production-grade security.

The most valuable ideas to import from NanoClaw are **scheduled tasks** (enabling proactive agents), **conversation accumulation** (richer group chat context), and **per-chat persistent memory** (long-term relationship with users). These fill genuine gaps in TeamAgentica's current feature set without compromising our architectural principles.
