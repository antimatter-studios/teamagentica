# storage-sss3

S3-compatible object storage powered by an embedded stupid-simple-s3 (sss3) subprocess. Wraps S3 with a higher-level REST interface, in-memory metadata index, trash support, and AI agent tools.

## Capabilities

- `storage:api` -- standard file interface (browse, read, write, delete)
- `storage:object` -- object storage operations
- `tool:storage:api` -- agent-callable file tools
- `tool:storage:object` -- agent-callable object tools

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `S3_ENDPOINT` | string | `http://sss3:9000` | S3 endpoint URL |
| `S3_BUCKET` | string | `teamagentica` | Bucket name |
| `S3_ACCESS_KEY` | string (secret) | `minioadmin` | S3 access key |
| `S3_SECRET_KEY` | string (secret) | `minioadmin` | S3 secret key |
| `S3_REGION` | string | `us-east-1` | S3 region |
| `PLUGIN_DEBUG` | boolean | `false` | Debug logging |

## API Endpoints

### Object Storage REST

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check (includes cached object count) |
| PUT | `/objects/*key` | Upload an object |
| GET | `/objects/*key` | Download an object (streams body) |
| DELETE | `/objects/*key` | Delete an object (moves to .trash/) |
| HEAD | `/objects/*key` | Get object metadata |
| POST | `/objects/copy` | Copy an object or folder |
| POST | `/objects/move` | Move/rename an object or folder |
| GET | `/download/zip?prefix=` | Download objects as zip archive |
| GET | `/browse?prefix=` | Directory-like listing from index cache |
| GET | `/list?prefix=` | Flat key listing from index cache |
| POST | `/refresh` | Force re-warm the metadata index |

### Trash

| Method | Path | Description |
|--------|------|-------------|
| GET | `/trash/browse?prefix=` | Browse deleted objects in .trash/ |
| POST | `/trash/restore` | Restore an object from .trash/ |
| POST | `/trash/empty` | Permanently delete from .trash/ |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/mcp` | Tool schema (9 tools) |
| POST | `/mcp/list_files` | List files/folders at a prefix |
| POST | `/mcp/read_file` | Read file content (text or base64) |
| POST | `/mcp/write_file` | Write/overwrite a file |
| POST | `/mcp/delete_file` | Delete a file (to .trash/) |
| POST | `/mcp/file_info` | Get file metadata without downloading |
| POST | `/mcp/create_folder` | Create a folder marker object |
| POST | `/mcp/browse_trash` | Browse trash |
| POST | `/mcp/restore_from_trash` | Restore from trash |
| POST | `/mcp/empty_trash` | Permanently delete from trash |

### S3 Proxy

All unmatched routes are reverse-proxied to the sss3 subprocess, so standard S3 clients work directly.

## Events

- **Emits:** `object_uploaded`, `object_deleted`, `object_copied`, `object_moved`, `folder_deleted`, `folder_copied`, `folder_moved`, `tool_*`
- **Subscribes:** `config:update`

## How It Works

1. Launches the `sss3` binary as a subprocess on a configurable port (default 5553).
2. Initializes an S3 client and ensures the bucket exists.
3. Warms an in-memory metadata index by listing all objects.
4. REST endpoints use the S3 client for reads/writes and update the index on mutations.
5. Deletes go to `.trash/` prefix -- copy to trash, then delete original.
6. Folders are empty marker objects with trailing `/` and content-type `application/x-directory`.
7. Registers tools with MCP server when `infra:mcp-server` becomes available.

## Notes

- If sss3 fails to start, the plugin runs in degraded mode (no storage operations).
- The metadata index is eventually consistent -- use `/refresh` to force sync.
- Text detection covers `text/*`, `application/json`, `application/xml`, `application/yaml`, etc.
- S3 credentials default to `minioadmin` for local development.
