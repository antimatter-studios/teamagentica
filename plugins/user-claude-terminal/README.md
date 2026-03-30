# user-claude-terminal

Workspace environment plugin that provides a web-based Claude Code CLI terminal. Serves a workspace schema that workspace-manager uses to spawn devbox containers with ttyd.

## Capability

- `workspace:environment`

## Dependency

- `workspace:manager`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `CLAUDE_SKIP_PERMISSIONS` | boolean | `false` | Run Claude Code with `--dangerously-skip-permissions` (auto-approves all tool use) |
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

`CLAUDE_SKIP_PERMISSIONS` is dynamically read from config on each schema request via `SchemaFunc`.

## Workspace Schema

Returned to workspace-manager when launching a container:

- **Image:** `teamagentica-devbox:latest`
- **Port:** `7681` (ttyd web terminal)
- **Shared mount:** `claude-shared` -> `/home/coder/.claude`
- **Env:** `DEVBOX_APP=claude`, `CLAUDE_SKIP_PERMISSIONS`, `TACLI_KERNEL=http://teamagentica-kernel:8080`

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/schema` | Plugin schema (config + workspace) |
| GET | `/health` | Health check |
| POST | `/events` | SDK event handler |

## Notes

- The plugin itself is a thin schema-serving container -- all terminal functionality lives in the devbox image.
- The shared mount persists Claude settings across workspace instances.
