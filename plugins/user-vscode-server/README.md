# user-vscode-server

VS Code in the browser -- full IDE with terminal, extensions, and git support.

## Overview

A workspace environment plugin that defines how to launch a code-server (VS Code) container. Serves a static workspace schema for workspace-manager. Unlike the terminal plugins, this uses a dedicated `code-server` image with the VS Code web UI on port 8080.

## Capabilities

- `workspace:environment`

## Dependencies

- `workspace:manager`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug mode |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |

## Events

None.

## How It Works

1. Registers with `workspace:environment` capability
2. Serves workspace schema (static):
   - Image: `teamagentica-code-server:latest`
   - Port: `8080` (code-server web UI)
   - Docker user: `coder`
   - Setup scripts: `code-server-navigator` (provisions Machine settings for navigator extension support)
   - Shared mounts: `code-server-shared/extensions` -> `/mnt/shared-extensions`, `claude-shared` -> `/home/coder/.claude`
   - Env: `DEFAULT_WORKSPACE=/workspace`, `XDG_DATA_HOME=/workspace/.code-server`
3. Workspace-manager uses this schema to launch VS Code instances

## Gotchas / Notes

- Uses `teamagentica-code-server:latest` instead of `teamagentica-devbox:latest`
- Port is `8080` (code-server default) not `7681` (ttyd)
- Two shared mounts: one for VS Code extensions (shared across all VS Code workspaces) and one for Claude settings
- The `code-server-navigator` setup script creates Machine-level VS Code settings enabling navigator extension support
- Runs as user `coder` inside the container
