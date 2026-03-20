# storage-sss3

S3-compatible object storage powered by stupid-simple-s3 (sss3).

## Overview

Runs an embedded sss3 subprocess that provides an S3-compatible API, then wraps it with a higher-level REST interface for object CRUD, directory browsing, and an AI agent tool interface. Maintains an in-memory metadata index that can be warmed from the bucket for fast browsing. Unmatched routes are reverse-proxied to the raw S3 API.

## Capabilities

- `storage:api`

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `S3_ENDPOINT` | string | yes | `http://sss3:9000` | S3 endpoint URL |
| `S3_BUCKET` | string | yes | `teamagentica` | Bucket name |
| `S3_ACCESS_KEY` | string | yes (secret) | `minioadmin` | S3 access key |
| `S3_SECRET_KEY` | string | yes (secret) | `minioadmin` | S3 secret key |
| `S3_REGION` | string | no | `us-east-1` | S3 region |
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug logging |

## API Endpoints

### Object Storage REST

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check (includes cached object count) |
| PUT | `/objects/*key` | Upload an object |
| GET | `/objects/*key` | Download an object (streams body) |
| DELETE | `/objects/*key` | Delete an object |
| HEAD | `/objects/*key` | Get object metadata |
| GET | `/browse?prefix=` | Directory-like listing from index cache |
| GET | `/list?prefix=` | Flat key listing from index cache |
| POST | `/refresh` | Force re-warm the metadata index |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tools` | Tool schema for agent discovery |
| POST | `/tool/list_files` | List files/folders at a prefix |
| POST | `/tool/read_file` | Read file content (text or base64) |
| POST | `/tool/write_file` | Write/overwrite a file |
| POST | `/tool/delete_file` | Delete a file |
| POST | `/tool/file_info` | Get file metadata without downloading |
| POST | `/tool/create_folder` | Create a folder marker object |

### S3 Proxy

All unmatched routes are reverse-proxied to the sss3 subprocess, so standard S3 clients work.

## Events

- **Emits:** `object_uploaded`, `object_deleted`, `tool_list_files`, `tool_read_file`, `tool_write_file`, `tool_delete_file`, `tool_file_info`, `tool_create_folder`
- **Subscribes:** `config:update`

## How It Works

1. On startup, launches the `sss3` binary as a subprocess on a configurable port (default 5553)
2. Initializes an S3 client pointing at the local sss3 instance and ensures the bucket exists
3. Warms an in-memory metadata index by listing all objects in the bucket
4. REST endpoints use the S3 client for reads/writes and update the index cache on mutations
5. Tool endpoints auto-detect text vs binary content types for read operations
6. Folders are represented as empty marker objects with trailing `/` and content-type `application/x-directory`

## Gotchas / Notes

- If sss3 fails to start, the plugin runs in degraded mode (no storage operations)
- The metadata index is eventually consistent -- use `/refresh` to force sync
- Text detection covers common types: `text/*`, `application/json`, `application/xml`, `application/yaml`, etc.
- S3 credentials have defaults (`minioadmin`) suitable for local development
