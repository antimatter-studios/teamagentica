# infra-workspace-manager

Orchestrates workspace environments -- discovers workspace plugins, manages container lifecycle, and creates isolated volumes. Central hub for developer workspaces, build/deploy/promote/rollback flows, and volume management.

## Capabilities

- `workspace:manager`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | `false` | Debug logging |

## API Endpoints

### Environments

| Method | Path | Description |
|--------|------|-------------|
| GET | `/environments` | List available workspace environment types |

### Workspaces

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/workspaces` | List all workspaces with status and URLs |
| POST | `/workspaces` | Create a workspace (name, environment_id, optional git_repo) |
| GET | `/workspaces/:id` | Get workspace details |
| PATCH | `/workspaces/:id` | Rename a workspace |
| DELETE | `/workspaces/:id` | Delete workspace (optional `?remove_volume=true`) |
| POST | `/workspaces/:id/start` | Start a stopped workspace |
| POST | `/workspaces/:id/persist` | Git add+commit+push workspace changes |

### Volumes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/volumes` | List volumes with size, tags, git info, active workspace flag |
| DELETE | `/volumes/:name` | Delete a volume (only if no active workspace) |

### Discord Commands

| Method | Path | Description |
|--------|------|-------------|
| POST | `/discord-command/workspace/list` | List workspaces with status |
| POST | `/discord-command/workspace/create` | Create a workspace |
| POST | `/discord-command/workspace/rename` | Rename a workspace |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/mcp` | Tool schema (8 tools) |
| POST | `/mcp/list_environments` | List available environments |
| POST | `/mcp/create_workspace` | Create a workspace |
| POST | `/mcp/list_workspaces` | List workspaces |
| POST | `/mcp/start_workspace` | Start a workspace |
| POST | `/mcp/rename_workspace` | Rename a workspace |
| POST | `/mcp/build_plugin` | Build a Docker image (routes to infra-builder) |
| POST | `/mcp/deploy_plugin` | Deploy a candidate container |
| POST | `/mcp/promote_plugin` | Promote candidate to primary |
| POST | `/mcp/rollback_plugin` | Stop candidate, revert to primary |

## How It Works

1. **Environment discovery:** Calls `sdk.SearchPlugins("workspace:environment")` to find installed workspace plugins, then fetches each plugin's `/schema` to get the `workspace` section (image, port, env, mounts).
2. **Workspace creation:** Generates an 8-char hex ID for permanent subdomain (`ws-{id}`), creates a volume at `/data/volumes/ws-{id}-{slug}`, optionally git clones, creates shared mounts, runs setup scripts, calls `sdk.CreateManagedContainer()`.
3. **Rename:** Updates display name and renames the volume directory slug. Subdomain is permanent.
4. **Delete:** Stops/removes the container via kernel API. Volume removed only with `?remove_volume=true`.
5. **Build/Deploy:** Routes build to the `build:docker` capability plugin; deploy/promote/rollback use SDK managed container APIs.

## Notes

- Volumes live at `/data/volumes/` -- cross-mounted from storage-disk's data.
- Subdomain format is `ws-{8hex}` and never changes, even on rename.
- Local SQLite DB only tracks `container_id -> environment_id` mapping.
- Workspace creation supports `plugin_source` for dev mode bind-mounts.
