# infra-workspace-manager

Orchestrates workspace environments -- discovers workspace plugins, manages container lifecycle, and creates isolated volumes.

## Overview

The central orchestrator for developer workspaces. Discovers installed workspace environment plugins (claude-terminal, codex-terminal, opencode-terminal, vscode-server), fetches their workspace schemas, and uses the kernel's managed containers API to launch, stop, rename, and delete workspace containers. Each workspace gets its own storage volume. Also supports git clone on creation, git persist (commit+push), volume management, and production-first deployment (build, deploy candidate, promote, rollback).

## Capabilities

- `workspace:manager`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug logging |

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
| GET | `/tools` | Tool schema (10 tools) |
| POST | `/tool/list_environments` | List available environments |
| POST | `/tool/create_workspace` | Create a workspace |
| POST | `/tool/list_workspaces` | List workspaces |
| POST | `/tool/start_workspace` | Start a workspace |
| POST | `/tool/rename_workspace` | Rename a workspace |
| POST | `/tool/build_plugin` | Build a Docker image (routes to infra-builder) |
| POST | `/tool/deploy_plugin` | Deploy a candidate container |
| POST | `/tool/promote_plugin` | Promote candidate to primary |
| POST | `/tool/rollback_plugin` | Stop candidate, revert to primary |

## Events

- **Emits:** `workspace:created`, `workspace:started`, `workspace:deleted`, `workspace:renamed`, `workspace:persisted`, `volume:deleted`

## How It Works

1. **Environment discovery:** Calls `sdk.SearchPlugins("workspace:environment")` to find installed workspace plugins, then fetches each plugin's `/schema` to get the `workspace` section (image, port, env, mounts, etc.)
2. **Workspace creation:**
   - Generates an 8-char random hex ID for permanent subdomain (`ws-{id}`)
   - Creates a volume directory at `/data/volumes/ws-{id}-{slug}`
   - Optionally `git clone`s a repo into the volume
   - Creates shared mount directories declared by the environment
   - Runs setup scripts (e.g. `code-server-navigator` for VS Code)
   - Calls `sdk.CreateManagedContainer()` with image, port, subdomain, volume, env, and mounts
   - Tracks workspace-environment mapping in local SQLite
3. **Rename:** Updates display name and renames the volume directory slug. Subdomain is permanent (never changes).
4. **Delete:** Stops/removes the container via kernel API. Optionally removes the volume.
5. **Build/Deploy:** Routes build requests to `infra-builder` plugin; deploy/promote/rollback use SDK managed container APIs for candidate container management.

## Gotchas / Notes

- Volumes live at `/data/volumes/` -- this is cross-mounted from `storage-volume`'s data
- Subdomain format is `ws-{8hex}` and never changes, even on rename
- The local SQLite DB only tracks `container_id -> environment_id` mapping (not duplicating kernel data)
- Volume listing detects tags (git repo, file extensions) via filesystem inspection
- Workspace creation supports `plugin_source` for dev mode (bind-mounts plugin source into the workspace)
- Deletion does NOT remove the volume by default -- pass `?remove_volume=true` to clean up
