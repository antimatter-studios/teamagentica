# TeamAgentica

A self-hosted, modular AI-orchestrated automation control platform.

## What Is This?

TeamAgentica is a governance-first control plane designed to coordinate AI agents and plugins while maintaining strict security boundaries. The AI agent is not the kernel — it's a constrained external operator.

The platform enforces authentication, RBAC, audit logging, and policy enforcement. All operational capabilities are implemented as versioned plugins that communicate over gRPC.

## Architecture

```
┌─────────────────────────────────────────────┐
│                  Frontend                    │
│            (React/TypeScript)                │
│         Static web app / Native app          │
│              JWT authentication              │
└──────────────────┬──────────────────────────┘
                   │ REST API (HTTP/JSON)
┌──────────────────▼──────────────────────────┐
│                  Kernel                      │
│                  (Go)                        │
│  ┌───────────┬──────────┬────────────────┐  │
│  │  JWT Auth │   RBAC   │  Plugin Reg.   │  │
│  ├───────────┼──────────┼────────────────┤  │
│  │  REST API │  Audit   │  Lifecycle Mgr │  │
│  └───────────┴──────────┴────────────────┘  │
│              SQLite (GORM)                   │
└──────────────────┬──────────────────────────┘
                   │ gRPC / Protobuf
        ┌──────────┼──────────┐
        ▼          ▼          ▼
   ┌─────────┐ ┌────────┐ ┌────────┐
   │Plugin A │ │Plugin B│ │Plugin C│
   │  (v1)   │ │  (v1)  │ │  (v2)  │
   └─────────┘ └────────┘ └────────┘
```

## Core Principles

1. The core is small, stable, and boring
2. All capabilities are implemented as plugins
3. AI agents are external clients, not trusted controllers
4. No component can grant itself additional authority
5. All actions are authenticated, authorized, and audited
6. Mechanical workflows are delegated to external systems

## Project Structure

```
teamagentica/
├── kernel/           # Go — core API, auth, RBAC, plugin management
├── user-interface/   # React/TS — web frontend
├── docs/             # Architecture docs and planning
└── docker-compose.yml
```

## Quick Start

```bash
docker compose up
```

- Kernel API: http://localhost:8080
- Frontend: http://localhost:3000

## Development

### Kernel (Go)

```bash
cd kernel
go run .
```

### Frontend (React)

```bash
cd user-interface
npm install
npm run dev
```

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `TEAMAGENTICA_KERNEL_HOST` | Kernel API host | `localhost` |
| `TEAMAGENTICA_KERNEL_PORT` | Kernel API port | `8080` |
| `TEAMAGENTICA_JWT_SECRET` | JWT signing secret | (required) |
| `TEAMAGENTICA_DB_PATH` | SQLite database path | `./database.db` |

## License

TBD
