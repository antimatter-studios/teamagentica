# infra-builder

Builds Docker images from source code stored in storage-volume volumes.

## Overview

Provides a Docker build service that agents and the platform can use to build container images. Takes source code from a named volume, tars it up as a build context, and streams the Docker build output as NDJSON. Used for production-first development workflows where agents build and deploy their own containers.

## Capabilities

- `infra:builder`

## Dependencies

- `storage:block` — volumes containing source code to build

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed build output |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/tools` | MCP tool discovery (single `build` tool) |
| POST | `/tool/build` | MCP tool entry point (delegates to `/build`) |
| POST | `/build` | Build a Docker image — streams NDJSON output |
| GET | `/builds` | List recent builds (up to 50) |
| GET | `/builds/:id/logs` | Get full build logs for a specific build |

## Events

- **Subscribes to:** none
- **Emits:** none

## How It Works

### Build flow

1. Receives `{volume, image, dockerfile, tag}` — volume is required, dockerfile defaults to `Dockerfile`, tag defaults to timestamp.
2. Resolves the volume path at `/workspaces/volumes/{name}`.
3. Creates a tar archive of the entire volume directory (excluding `.git/`) as the Docker build context.
4. Calls Docker Engine API `ImageBuild` with target stage `prod`, tagging as `image:tag`.
5. Streams Docker build output as NDJSON lines (`{"stream":"..."}`) with a final result or error object.
6. Stores the build record (ID, status, duration, logs) in memory.

### Build serialization

Only one build runs at a time. Concurrent requests get a 409 Conflict response. This prevents resource contention on the Docker daemon.

### Build history

Up to 50 build records are kept in memory (not persisted to disk). Each record includes status (`building`/`success`/`failed`), duration, and error message. Full logs are available via `/builds/:id/logs`.

## Gotchas / Notes

- Build IDs are timestamp-based (`YYYYMMDD-HHMMSS`), not UUIDs — two builds in the same second would collide (unlikely given serialization).
- The `prod` target stage is hardcoded in the Docker build options — Dockerfiles must have a `prod` stage or the build will fail.
- Build history is in-memory only — restarts lose all build records.
- The tar context includes everything in the volume except `.git/` — large volumes with `node_modules` or similar will create large build contexts.
- Requires Docker socket access (`/var/run/docker.sock`) mounted into the container.
