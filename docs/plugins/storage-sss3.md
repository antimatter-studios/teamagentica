# storage-sss3

> S3-compatible file storage with in-memory index and browse API.

## Overview

The storage-sss3 plugin provides S3-compatible object storage for the platform. It manages a `stupid-simple-s3` subprocess and wraps it with a REST API, in-memory metadata index, and browse functionality. Other plugins use it through the SDK's storage helpers.

## Capabilities

- `storage:api` — S3-compatible storage API

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `S3_ENDPOINT` | string | no | `http://sss3:9000` | S3 endpoint URL |
| `S3_BUCKET` | string | no | `teamagentica` | S3 bucket name |
| `S3_ACCESS_KEY` | string (secret) | no | `minioadmin` | S3 access key |
| `S3_SECRET_KEY` | string (secret) | no | `minioadmin` | S3 secret key |
| `S3_REGION` | string | no | `us-east-1` | S3 region |
| `SSS3_STORAGE_PORT` | int | no | `8081` | Plugin HTTP port |
| `SSS3_PORT` | int | no | `5553` | sss3 subprocess S3 port |
| `SSS3_STORAGE_PATH` | string | no | `/data/sss3` | Storage data directory |
| `PLUGIN_DEBUG` | boolean | no | `false` | Verbose logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (cached object count) |
| `PUT` | `/objects/*key` | Upload object (Content-Type from header) |
| `GET` | `/objects/*key` | Download object (streams with Content-Type + ETag) |
| `DELETE` | `/objects/*key` | Delete object |
| `HEAD` | `/objects/*key` | Object metadata (Content-Type, ETag, Content-Length, Last-Modified) |
| `GET` | `/browse?prefix=` | Filesystem-like listing (folders + files) |
| `GET` | `/list?prefix=` | Flat object listing |
| `POST` | `/refresh` | Force full re-scan of sss3 |
| `*` | catch-all | Raw S3 protocol passthrough to sss3 |

## Events

### Emissions

- `object_uploaded` — When an object is uploaded
- `object_deleted` — When an object is deleted

## Usage

### SDK Storage Helpers

Other plugins access storage through the SDK without knowing the storage plugin ID:

```go
err := client.StorageWrite(ctx, "media/image.png", reader, "image/png")
body, contentType, err := client.StorageRead(ctx, "media/image.png")
```

### Browse API

`GET /browse?prefix=media/` returns:
```json
{
  "prefix": "media/",
  "folders": ["generated/"],
  "files": [{"key": "media/image.png", "size": 12345, ...}]
}
```

### Architecture

- Manages `stupid-simple-s3` as a subprocess (waits up to 15s for health check)
- In-memory metadata index (`sync.RWMutex` map) for fast browse/list without S3 roundtrips
- Index warmed on startup via `ListObjectsV2`; refresh via `POST /refresh`
- If sss3 subprocess crashes, the plugin exits (`os.Exit(1)`)

## Related

- [Plugin SDK](../plugin-sdk.md) — Storage helper API
- [messaging-chat](messaging-chat.md) — Stores media attachments
- [mcp-server](infra-mcp-server.md) — Stores generated media
