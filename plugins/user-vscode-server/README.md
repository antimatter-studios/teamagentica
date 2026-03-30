# user-vscode-server

Workspace environment plugin that provides VS Code in the browser via code-server. Serves a workspace schema that workspace-manager uses to spawn code-server containers with a full IDE.

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

- **Image:** `teamagentica-code-server:latest` (not devbox)
- **Port:** `8080` (code-server web UI)
- **Docker user:** `coder`
- **Setup scripts:** `code-server-navigator` (provisions Machine settings for navigator extension)
- **Shared mounts:**
  - `code-server-shared/extensions` -> `/mnt/shared-extensions`
  - `claude-shared` -> `/home/coder/.claude`
- **Env:** `DEFAULT_WORKSPACE=/workspace`, `XDG_DATA_HOME=/workspace/.code-server`

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/schema` | Plugin schema (config + workspace) |
| GET | `/health` | Health check |
| POST | `/events` | SDK event handler |

## Notes

- Uses `teamagentica-code-server:latest` instead of devbox -- different base image with VS Code web UI.
- Uses static `Schema` field (no `SchemaFunc`).
- Two shared mounts: one for VS Code extensions (shared across all VS Code workspaces) and one for Claude settings.
- Runs as user `coder` inside the container.
