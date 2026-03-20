# builtin-provider

Default marketplace provider serving the official plugin catalog.

## Overview

A package manager / catalog service. Stores versioned plugin manifests (full plugin.yaml content) in a SQLite database and serves them for marketplace browsing and plugin installation. Manifests are submitted via POST and retrieved by plugin ID (latest semver version returned). This is the **only** thing it does -- once a plugin is installed, you never ask the builtin-provider about it again.

## Capabilities

None declared.

## Dependencies

None.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/plugins?q=` | Browse catalog entries (optional search query) |
| GET | `/plugins/:id/manifest` | Full manifest for a plugin (latest version) |
| POST | `/manifests` | Submit/upsert a plugin manifest (requires `id` and `version`) |

## Events

None.

## How It Works

1. On startup, opens a SQLite database at `/data/catalog.db` (or `CATALOG_DB` env var)
2. The `manifests` table stores versioned snapshots: `(plugin_id, version, data_json)`
3. `POST /manifests` upserts by plugin_id + version -- this is how manifests enter the catalog
4. `GET /plugins` returns browsing entries (id, name, description, group, version, image, tags, capabilities, dependencies) for the latest version of each plugin, optionally filtered by search query
5. `GET /plugins/:id/manifest` returns the full manifest JSON for the latest semver version
6. Version comparison uses proper semver (dot-separated numeric components)

## Gotchas / Notes

- **Catalog is reference-only** -- it tells the TUI what's available to install. Plugin runtime data (config, schema, status) always comes from the running plugin itself, never from this catalog
- No manifests are baked into the Docker image -- all data enters via `POST /manifests`
- Group definitions (agents, messaging, tools, storage, network, infrastructure, user) are hardcoded in the store for display ordering
- Port defaults to 8083 (via `PROVIDER_PORT` env var)
