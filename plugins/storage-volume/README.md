# storage-volume

Namespace-isolated disk storage volumes with metadata and type labels.

## Overview

Manages persistent filesystem volumes under a configurable path. Each volume is a directory with associated JSON metadata (name, type, labels, creation time). Provides both a volume management API (create/list/delete) and a standard `storage:api` file interface (browse, read, write, delete). Also serves Discord slash commands for volume management and AI agent tool endpoints.

## Capabilities

- `storage:api`
- `storage:disk`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `STORAGE_VOLUMES_PATH` | string | no | `/data/volumes` | Path for volume directories |
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug logging |

## API Endpoints

### storage:api (file interface on dataPath)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with disk usage stats |
| GET | `/browse?prefix=` | Directory listing |
| PUT | `/objects/*key` | Upload a file |
| GET | `/objects/*key` | Download a file |
| DELETE | `/objects/*key` | Delete a file |
| HEAD | `/objects/*key` | File metadata headers |

### storage:disk (volume management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/volumes` | Create a volume (name, type, labels) |
| GET | `/volumes` | List volumes (optional `?type=` filter) |
| GET | `/volumes/:name` | Get volume detail (metadata + size + path) |
| GET | `/volumes/:name/path` | Get filesystem path for a volume |
| DELETE | `/volumes/:name` | Delete a volume and its contents |

### Discord Commands

| Method | Path | Description |
|--------|------|-------------|
| POST | `/discord-command/volume/list` | List volumes with sizes |
| POST | `/discord-command/volume/create` | Create a volume |
| POST | `/discord-command/volume/rename` | Rename a volume |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tools` | Tool schema (volume + file tools) |
| POST | `/tool/create_volume` | Create a volume |
| POST | `/tool/list_volumes` | List volumes |
| POST | `/tool/delete_volume` | Delete a volume |
| POST | `/tool/list_files` | List files at a prefix |
| POST | `/tool/read_file` | Read a file |
| POST | `/tool/write_file` | Write a file |
| POST | `/tool/delete_file` | Delete a file |

## Events

- **Subscribes:** `config:update`

## How It Works

- Volumes are directories under `STORAGE_VOLUMES_PATH` (default `/data/volumes`)
- Metadata JSON files are stored separately under `{dataPath}/meta/{name}.json` to keep volume dirs clean for bind-mounting
- `ListVolumes` discovers volumes from both metadata files and bare directories (so externally-created volumes are visible)
- Volume types are `auth` (for credentials) or `storage` (general purpose)
- Volume names: 1-128 chars, alphanumeric + hyphens/underscores/dots, must start with alphanumeric
- Disk usage stats come from `statfs` syscall
- Path traversal is prevented via `filepath.Clean` + prefix validation

## Gotchas / Notes

- `storage:api` file operations operate on `dataPath` (the general data directory), not on individual volumes
- Volume listing returns `size_bytes` computed by walking the directory tree -- can be slow for large volumes
- The `storage:disk` capability is how other plugins (like workspace-manager) discover this plugin
- Discord commands register via the SDK `DiscordCommands` field at startup
