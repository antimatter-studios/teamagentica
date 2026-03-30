# storage-disk

Namespace-isolated disk storage with metadata and type labels. Provides both disk management and a standard file interface with trash/restore support.

## Capabilities

- `storage:api` -- standard file interface (browse, read, write, delete)
- `storage:disk` -- disk management (create, list, delete named disks)
- `tool:storage:api` -- agent-callable file tools
- `tool:storage:disk` -- agent-callable disk tools

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `STORAGE_DISKS_PATH` | string | `/data/disks` | Path for disk directories |
| `PLUGIN_DEBUG` | boolean | `false` | Debug logging |

## API Endpoints

### storage:api (file interface on dataPath)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with disk usage stats |
| GET | `/browse?prefix=` | Directory listing |
| PUT | `/objects/*key` | Upload a file |
| GET | `/objects/*key` | Download a file |
| DELETE | `/objects/*key` | Delete a file (moves to .Trash) |
| HEAD | `/objects/*key` | File metadata headers |
| POST | `/objects/copy` | Copy a file or folder |
| POST | `/objects/move` | Move/rename a file or folder |
| GET | `/download/zip?prefix=` | Download a directory as zip |

### Trash

| Method | Path | Description |
|--------|------|-------------|
| GET | `/trash/browse?prefix=` | Browse deleted files in .Trash |
| POST | `/trash/restore` | Restore a file from .Trash |
| POST | `/trash/empty` | Permanently delete from .Trash |

### storage:disk (disk management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/disks` | Create a disk (name, type, labels) |
| GET | `/disks` | List disks (optional `?type=` filter) |
| GET | `/disks/:name` | Get disk detail (metadata + size + path) |
| GET | `/disks/:name/path` | Get filesystem path for a disk |
| DELETE | `/disks/:name` | Delete a disk (moves to .Trash) |

### Discord Commands

| Method | Path | Description |
|--------|------|-------------|
| POST | `/discord-command/disk/list` | List disks with sizes |
| POST | `/discord-command/disk/create` | Create a disk |
| POST | `/discord-command/disk/rename` | Rename a disk |

### AI Agent Tool Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/mcp` | Tool schema (10 tools: disk + file + trash) |
| POST | `/mcp/create_disk` | Create a disk |
| POST | `/mcp/list_disks` | List disks |
| POST | `/mcp/delete_disk` | Delete a disk |
| POST | `/mcp/list_files` | List files at a prefix |
| POST | `/mcp/read_file` | Read a file (text or base64) |
| POST | `/mcp/write_file` | Write a file |
| POST | `/mcp/delete_file` | Delete a file (to .Trash) |
| POST | `/mcp/browse_trash` | Browse trash |
| POST | `/mcp/restore_from_trash` | Restore from trash |
| POST | `/mcp/empty_trash` | Permanently delete from trash |

## Events

- **Subscribes:** `config:update`

## How It Works

- Disks are directories under `STORAGE_DISKS_PATH` (default `/data/disks`).
- Metadata JSON files stored separately under `{dataPath}/meta/{name}.json` to keep disk dirs clean for bind-mounting.
- `ListDisks` discovers disks from both metadata files and bare directories (externally-created disks are visible).
- Disk types: `auth` (credentials) or `storage` (general purpose).
- Deletes go to `.Trash` first -- files can be browsed, restored, or permanently emptied.
- Registers tools with MCP server when `infra:mcp-server` becomes available.

## Notes

- `storage:api` file operations work on `dataPath` (general data directory), not individual disks.
- Disk listing computes `size_bytes` by walking directory tree -- can be slow for large disks.
- Path traversal prevented via `filepath.Clean` + prefix validation + symlink resolution.
