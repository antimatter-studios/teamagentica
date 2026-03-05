# TeamAgentica Architecture

## Overview

TeamAgentica is a modular automation control platform composed of independently deployable components that communicate over well-defined protocols.

## Components

### Kernel (Go Binary)

The kernel is the central authority. It is intentionally minimal.

**Responsibilities:**
- JWT-based authentication (issue, validate, refresh tokens)
- RBAC with capability-encoded JWT tokens
- Plugin registry (SQLite-backed)
- Plugin lifecycle management (install, enable, disable, upgrade, rollback)
- REST API surface for all operations
- Audit logging
- Database migrations via GORM

**Does NOT do:**
- Infrastructure logic
- AI reasoning
- Workflow execution
- UI serving

**Token Types:**
- User tokens — issued on login, carry user capabilities
- Service tokens — pre-provisioned for automated processes
- Named tokens — user-generated, handed to processes by reference name

### User Interface (React/TypeScript)

A standalone web application. It is just another API client.

- Connects to kernel REST API via `TEAMAGENTICA_KERNEL_HOST` and `TEAMAGENTICA_KERNEL_PORT`
- Authenticates with JWT
- Renders UI, manages local state
- Completely decoupled from kernel deployment
- Could be replaced by a native app, CLI, or any HTTP client

### Plugins (Any Language)

Plugins are separate executables that communicate with the kernel over gRPC/protobuf.

- Each plugin is a standalone process
- Registered in the kernel's plugin registry
- Kernel launches enabled plugins on boot
- Plugins report health via gRPC health check
- Versioned — v2 can run alongside v1
- Traffic routing by user identity (canary testing)

**Plugin Manifest declares:**
- Plugin ID, name, version
- gRPC endpoint
- Required capabilities/permissions
- Dependencies on other plugins
- Health check configuration

### Marketplace

A special plugin type that provides a catalog of available plugins.

- Default marketplace ships with the system (non-removable)
- Additional marketplaces can be added (trusted/untrusted)
- All plugins (including "first-party") come from a marketplace
- No first-party vs third-party distinction in code
- Trust flows from marketplace to its plugins

## Communication

```
Frontend ←→ Kernel:    REST/HTTP + JWT
Kernel  ←→ Plugins:    gRPC/Protobuf
Kernel  ←→ Database:   SQLite via GORM
```

## Authentication Flow

1. User submits credentials to kernel REST API
2. Kernel validates against SQLite user store
3. Kernel issues JWT with encoded capabilities
4. Frontend stores JWT, sends in Authorization header
5. Kernel middleware validates JWT on every request
6. Capabilities extracted from JWT claims for RBAC decisions

## Plugin Lifecycle

1. User requests plugin install from a marketplace
2. Kernel fetches manifest from marketplace
3. Binary/image pulled and stored locally
4. Registry entry created (disabled by default)
5. User enables plugin
6. Kernel launches plugin process, waits for health check
7. Plugin registers its gRPC services
8. Kernel routes requests to plugin as needed

## Canary/Test Routing

When a v2 plugin is deployed alongside v1:
- Routing rules can target specific users (by JWT claims)
- e.g., `chris.test@teamagentica.io` → v2, everyone else → v1
- Allows production testing without affecting all users
- Kernel manages routing table per-plugin

## Deployment

- Docker Compose for development and single-host
- Kubernetes for production (future)
- Each component is its own container
- Kernel manages plugin containers (or processes in single-host mode)
