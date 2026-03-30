# system-teamagentica-plugin-provider

Package manager / catalog service for the plugin marketplace. Stores versioned plugin manifests in SQLite and serves them for browsing and installation. This is the only thing it does -- once a plugin is installed, the running plugin itself provides all runtime data.

## Capabilities

None declared.

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `PLUGIN_DEBUG` | boolean | no | `false` | Debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/plugins?q=` | Browse catalog entries (optional search query, latest version per plugin) |
| GET | `/plugins/:id/manifest` | Full manifest JSON for a plugin (latest semver version) |
| POST | `/manifests` | Submit/upsert a plugin manifest (requires `id` and `version` fields) |
| DELETE | `/plugins/:id` | Remove all versions of a plugin from the catalog |

## Storage

SQLite at `/data/catalog.db` via GORM. The `manifests` table stores `(plugin_id, version, data_json)` with a unique index on plugin_id+version. Soft deletes enabled.

## How It Works

1. `POST /manifests` upserts by plugin_id + version -- this is how manifests enter the catalog
2. `GET /plugins` returns browsing entries (id, name, description, group, version, image, tags, capabilities, dependencies) for the latest semver version of each plugin
3. `GET /plugins/:id/manifest` returns the full manifest JSON for the latest semver version
4. Version comparison uses proper semver (dot-separated numeric components)
5. Group definitions (agents, messaging, tools, storage, network, infrastructure, user) are hardcoded for display ordering

## Notes

- Catalog is reference-only -- tells the UI what's available to install. Runtime data always comes from the running plugin.
- No manifests are baked into the Docker image -- all data enters via `POST /manifests`
- Port hardcoded to 8083
