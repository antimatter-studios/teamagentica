# user-claude-terminal

Web terminal with Claude Code CLI -- AI-powered coding assistant in the browser.

## Overview

A workspace environment plugin that defines how to launch a Claude Code terminal container. Does not run a complex server itself -- it registers with the kernel and serves a workspace schema that the workspace-manager reads to spawn containers. The actual terminal runs in a `teamagentica-devbox` container with ttyd on port 7681.

## Capabilities

- `workspace:environment`

## Dependencies

- `workspace:manager`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `CLAUDE_SKIP_PERMISSIONS` | boolean | no | `false` | Run Claude Code with `--dangerously-skip-permissions` |
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug mode |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |

## Events

None.

## How It Works

1. Plugin registers with the kernel, advertising `workspace:environment` capability
2. Serves a `workspace` schema via `SchemaFunc` containing:
   - Image: `teamagentica-devbox:latest`
   - Port: `7681` (ttyd web terminal)
   - Shared mount: `claude-shared` -> `/home/coder/.claude`
   - Env: `DEVBOX_APP=claude`, `CLAUDE_SKIP_PERMISSIONS` from config
3. When a user creates a workspace with this environment, workspace-manager fetches the schema and launches a container with these settings
4. The devbox image handles installing/running Claude Code CLI

## Gotchas / Notes

- The plugin itself is a thin schema-serving container -- all actual terminal functionality lives in the devbox image
- `CLAUDE_SKIP_PERMISSIONS` is dynamically read from config on each schema request
- The shared mount (`claude-shared`) persists Claude settings across workspace instances
