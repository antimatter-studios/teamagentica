# storage-disk

Namespace-isolated disk storage disks with metadata and type labels.

## Overview

Manages persistent filesystem disks under a configurable path. Each disk is a directory with associated JSON metadata (name, type, labels, creation time). Provides both a disk management API (create/list/delete) and a standard `storage:api` file interface (browse, read, write, delete). Also serves Discord slash commands for disk management and AI agent tool endpoints.

## Capabilities

- `storage:api`
- `storage:disk`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `STORAGE_DISKS_PATH` | string | no | `/data/disks` | Path for disk directories |
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

### storage:disk (disk management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/disks` | Create a disk (name, type, labels) |
| GET | `/disks` | List disks (optional `?type=` filter) |
| GET | `/disks/:name` | Get disk detail (metadata + size + path) |
| GET | `/disks/:name/path` | Get filesystem path for a disk |
| DELETE | `/disks/:name` | Delete a disk and its contents |

### Discord Commands

| Method | Path | Description |
|--------|------|-------------|
| POST | `/discord-command/disk/list` | List disks with sizes |
| POST | `/discord-command/disk/create` | Create a disk |
| POST | `/discord-command/disk/rename` | Rename a disk |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tools` | Tool schema (disk + file tools) |
| POST | `/mcp/create_disk` | Create a disk |
| POST | `/mcp/list_disks` | List disks |
| POST | `/mcp/delete_disk` | Delete a disk |
| POST | `/mcp/list_files` | List files at a prefix |
| POST | `/mcp/read_file` | Read a file |
| POST | `/mcp/write_file` | Write a file |
| POST | `/mcp/delete_file` | Delete a file |

## Events

- **Subscribes:** `config:update`

## How It Works

- Disks are directories under `STORAGE_DISKS_PATH` (default `/data/disks`)
- Metadata JSON files are stored separately under `{dataPath}/meta/{name}.json` to keep disk dirs clean for bind-mounting
- `ListDisks` discovers disks from both metadata files and bare directories (so externally-created disks are visible)
- Disk types are `auth` (for credentials) or `storage` (general purpose)
- Disk names: 1-128 chars, alphanumeric + hyphens/underscores/dots, must start with alphanumeric
- Disk usage stats come from `statfs` syscall
- Path traversal is prevented via `filepath.Clean` + prefix validation

## Gotchas / Notes

- `storage:api` file operations operate on `dataPath` (the general data directory), not on individual disks
- Disk listing returns `size_bytes` computed by walking the directory tree -- can be slow for large disks
- The `storage:disk` capability is how other plugins (like workspace-manager) discover this plugin
- Discord commands register via the SDK `DiscordCommands` field at startup
