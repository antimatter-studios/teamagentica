# user-opencode-terminal

Web terminal with OpenCode CLI -- AI-powered coding assistant in the browser.

## Overview

A workspace environment plugin that defines how to launch an OpenCode terminal container. Serves a static workspace schema for workspace-manager.

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
2. Serves workspace schema (static, via `Schema` field):
   - Image: `teamagentica-devbox:latest`
   - Port: `7681`
   - Shared mount: `opencode-shared` -> `/home/coder/.opencode`
   - Env: `DEVBOX_APP=opencode`
3. Workspace-manager uses this schema to launch OpenCode terminal containers

## Gotchas / Notes

- Unlike claude-terminal and codex-terminal, this plugin uses the static `Schema` field instead of `SchemaFunc` since it has no dynamic config to inject
- Simplest of the terminal plugins -- no special config knobs
