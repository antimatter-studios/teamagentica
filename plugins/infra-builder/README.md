# infra-builder

Builds Docker images from source code stored in storage-disk volumes. Provides a single `build` tool for agents and a REST API for direct use.

## Capabilities

- `infra:builder`

## Dependencies

- `storage:disk` -- volumes containing source code to build

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed build output |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/mcp` | Tool definitions (single `build` tool) |
| POST | `/mcp/build` | Tool entry point for build (delegates to `/build`) |
| POST | `/build` | Build a Docker image -- streams NDJSON output |
| GET | `/builds` | List recent builds (up to 50) |
| GET | `/builds/:id/logs` | Get full build logs for a specific build |

## How It Works

1. Receives `{volume, image, dockerfile, tag}` -- volume and image required, dockerfile defaults to `Dockerfile`, tag defaults to timestamp.
2. Resolves the volume path at `/workspaces/volumes/{name}`.
3. Creates a tar archive of the entire volume directory (excluding `.git/`) as the Docker build context.
4. Calls Docker Engine API `ImageBuild` with target stage `prod`, tagging as `image:tag`.
5. Streams Docker build output as NDJSON lines with a final result or error object.
6. Stores the build record (ID, status, duration, logs) in memory.

Only one build runs at a time -- concurrent requests get 409 Conflict. Build history (up to 50 records) is in-memory only.

## Notes

- Build IDs are timestamp-based (`YYYYMMDD-HHMMSS`), not UUIDs.
- The `prod` target stage is hardcoded -- Dockerfiles must have a `prod` stage.
- Build history is lost on restart.
- Requires Docker socket access (`/var/run/docker.sock`).
