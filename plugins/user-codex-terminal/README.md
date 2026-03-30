# user-codex-terminal

Workspace environment plugin that provides a web-based OpenAI Codex CLI terminal. Serves a workspace schema that workspace-manager uses to spawn devbox containers with ttyd.

## Capability

- `workspace:environment`

## Dependency

- `workspace:manager`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `CODEX_APPROVAL_MODE` | select | `suggest` | `suggest` (asks before everything), `auto-edit` (auto-approves file changes), `full-auto` (auto-approves everything) |
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

`CODEX_APPROVAL_MODE` is dynamically read from config on each schema request via `SchemaFunc`.

## Workspace Schema

Returned to workspace-manager when launching a container:

- **Image:** `teamagentica-devbox:latest`
- **Port:** `7681` (ttyd web terminal)
- **Shared mount:** `codex-shared` -> `/home/coder/.codex`
- **Env:** `DEVBOX_APP=codex`, `CODEX_APPROVAL_MODE`, `TACLI_KERNEL=http://teamagentica-kernel:8080`

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/schema` | Plugin schema (config + workspace) |
| GET | `/health` | Health check |
| POST | `/events` | SDK event handler |

## Notes

- The plugin itself is a thin schema-serving container -- all terminal functionality lives in the devbox image.
- The shared mount persists Codex settings across workspace instances.
