# user-opencode-terminal

Workspace environment plugin that provides a web-based OpenCode CLI terminal. Serves a workspace schema that workspace-manager uses to spawn devbox containers with ttyd.

## Capability

- `workspace:environment`

## Dependency

- `workspace:manager`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Enable debug logging |

## Workspace Schema

Returned to workspace-manager when launching a container:

- **Image:** `teamagentica-devbox:latest`
- **Port:** `7681` (ttyd web terminal)
- **Shared mount:** `opencode-shared` -> `/home/coder/.opencode`
- **Env:** `DEVBOX_APP=opencode`, `TACLI_KERNEL=http://teamagentica-kernel:8080`

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/schema` | Plugin schema (config + workspace) |
| GET | `/health` | Health check |
| POST | `/events` | SDK event handler |

## Notes

- The plugin itself is a thin schema-serving container -- all terminal functionality lives in the devbox image.
- Uses static `Schema` field (no `SchemaFunc`) since there are no dynamic config values to inject.
- Simplest of the terminal plugins -- no special config knobs.
