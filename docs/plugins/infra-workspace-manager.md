# infra-workspace-manager

> Manages browser-based development workspaces with persistent volumes and multi-environment support.

## Overview
Creates and manages cloud IDE workspaces (VS Code, etc.) backed by persistent Docker volumes. Handles workspace lifecycle, volume management, and project detection.

## Capabilities
- `workspace:manager`

## Config
| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `WORKSPACE_MANAGER_PORT` | number | 8091 | Listen port |
| `PLUGIN_DEBUG` | boolean | false | Debug logging |

## API Endpoints

### Environments
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/environments` | List available workspace environment types |

### Workspaces
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/workspaces` | List active workspaces |
| `POST` | `/workspaces` | Create workspace (name, environment_id, git_repo optional) |
| `GET` | `/workspaces/:id` | Get workspace details |
| `PATCH` | `/workspaces/:id` | Rename workspace (updates display name and volume slug) |
| `DELETE` | `/workspaces/:id` | Delete workspace and container |
| `POST` | `/workspaces/:id/persist` | Persist workspace (git commit/push) |

### Volumes
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/volumes` | List all volumes with tags, git info, extensions |
| `DELETE` | `/volumes/:name` | Delete a volume (must not be in use) |

## Naming Scheme
- Workspace ID: `ws-{8hex}` — random, permanent, used as subdomain
- Volume name: `ws-{8hex}-{slug}` — slug derived from display name, changes on rename
- Container name: `ws-{8hex}` — matches workspace ID
- Display name: free text, user-facing

## Volume Tags
Scans workspace root directory and detects:
- Languages: go, node, typescript, python, rust, ruby, java, php
- Frameworks: react, next, vue, nuxt, svelte, angular, vite
- Tools: git, docker
- Static: html

Also detects installed VS Code extensions from `.code-server/extensions/`.

## Code-Server Settings Provisioning

On workspace creation, the manager provisions Machine settings in the volume at `.code-server/code-server/Machine/settings.json`. Currently enables `extensions.supportNodeGlobalNavigator` for extensions (e.g. Claude Code) that access the Node.js `navigator` global (required since code-server 4.110+ / Node v22). The `XDG_DATA_HOME` env var is set to `/workspace/.code-server` so code-server picks up these settings.

## Architecture
- Discovers workspace environments by searching plugins with `workspace:environment` capability
- Creates containers via kernel's managed container API
- Volumes stored at `/workspaces/volumes/{volume_name}/`
- Workspaces accessible via path-based routing at `/ws/{container_id}/` through the kernel proxy
- Subdomain routing via Docker proxy labels also works for local development
