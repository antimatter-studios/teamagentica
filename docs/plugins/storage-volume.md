# storage-volume

> Local filesystem storage with S3-compatible API and AI agent tool interface.

## Overview

The Volume Storage plugin provides file storage backed by a local filesystem path (typically a Docker volume mount). It exposes an S3-compatible object API for other plugins and a tool interface that allows AI agents to discover and use file operations. Supports browsing, reading, writing, and deleting files with automatic MIME type detection and directory traversal protection.

## Capabilities

- `storage:api` — S3-compatible storage API
- `storage:volume` — Volume-backed storage
- `tool:storage` — File operation tools for AI agents

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `STORAGE_DATA_PATH` | string | no | `/data` | Local filesystem path for volume storage |
| `STORAGE_VOLUME_PORT` | string | no | `8090` | HTTP port for the storage plugin |
| `PLUGIN_ALIASES` | aliases | no | — | Alias configuration |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log detailed operations |

## API Endpoints

### Storage API (S3-compatible)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/usage` | Disk usage statistics |
| `GET` | `/dirs` | List directories with sizes |
| `GET` | `/browse` | Browse files in a directory |
| `PUT` | `/objects/*key` | Upload/write a file |
| `GET` | `/objects/*key` | Download/read a file |
| `DELETE` | `/objects/*key` | Delete a file |
| `HEAD` | `/objects/*key` | File metadata (size, content type) |

### Tool Interface (for AI agents)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/tools` | List available tool schemas |
| `POST` | `/tool/list_files` | List files in a directory |
| `POST` | `/tool/read_file` | Read file contents (text as-is, binary as base64) |
| `POST` | `/tool/write_file` | Write content to a file |
| `POST` | `/tool/delete_file` | Delete a file |
| `POST` | `/tool/file_info` | Get file metadata |
| `POST` | `/tool/create_folder` | Create a directory |

## Events

### Subscriptions

- `kernel:alias:update` — Hot-swaps alias map (debounced 2s)
- `config:update` — Reloads configuration

## Security

- **Directory traversal protection**: All file paths are resolved against the configured data path; attempts to escape via `../` are blocked.
- **Content encoding**: Text files are returned as-is; binary files are base64-encoded in tool responses.
- **MIME detection**: Automatic content type detection for served files.

## Related

- [Plugin SDK](../plugin-sdk.md) — Storage helpers (`StorageWrite`, `StorageRead`, etc.)
