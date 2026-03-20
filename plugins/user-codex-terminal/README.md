# user-codex-terminal

Web terminal with OpenAI Codex CLI -- AI-powered coding assistant in the browser.

## Overview

A workspace environment plugin that defines how to launch an OpenAI Codex terminal container. Serves a workspace schema for workspace-manager to consume. The terminal runs in a `teamagentica-devbox` container with ttyd.

## Capabilities

- `workspace:environment`

## Dependencies

- `workspace:manager`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `CODEX_APPROVAL_MODE` | select | no | `suggest` | Approval mode: `suggest`, `auto-edit`, `full-auto` |
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug mode |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |

## Events

None.

## How It Works

1. Registers with `workspace:environment` capability
2. Serves workspace schema:
   - Image: `teamagentica-devbox:latest`
   - Port: `7681`
   - Shared mount: `codex-shared` -> `/home/coder/.codex`
   - Env: `DEVBOX_APP=codex`, `CODEX_APPROVAL_MODE` from config
3. Workspace-manager uses this schema to launch Codex terminal containers

## Gotchas / Notes

- `CODEX_APPROVAL_MODE` controls how much autonomy Codex has: `suggest` (asks before everything), `auto-edit` (auto-approves file changes), `full-auto` (auto-approves everything)
- Config value is dynamically read on each schema request
