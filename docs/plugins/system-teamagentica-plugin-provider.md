# system-teamagentica-plugin-provider

> Default plugin catalog for the marketplace.

## Overview

The system-teamagentica-plugin-provider plugin serves a hardcoded catalog of all official TeamAgentica plugins. It powers the marketplace UI, enabling plugin discovery and installation. This is a system plugin that cannot be disabled.

## Capabilities

- `marketplace:provider` — Plugin catalog provider

## Configuration

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `PROVIDER_PORT` | int | no | `8083` | HTTP port |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/plugins?q=` | Search catalog (substring match on ID, name, description, tags) |

## Events

None — no subscriptions or emissions.

## Usage

### Catalog Search

`GET /plugins?q=telegram` returns matching entries. Empty query returns all plugins.

### Catalog Entries

Each entry includes:
- Plugin ID, name, description, version
- Docker image name
- Author and tags
- Config schema (field types, labels, defaults, secrets, visibility conditions)
- Default pricing (per 1M tokens or per request)

### How Installation Works

1. User browses marketplace in the UI
2. Frontend calls `GET /api/marketplace/plugins?q=...` on the kernel
3. Kernel fans out to all enabled providers (including this one)
4. User selects a plugin and clicks install
5. Kernel calls `POST /api/marketplace/install` which fetches the catalog entry, creates the plugin record, generates a service token, and seeds default pricing

### Current Catalog

18 plugins: agent-openai, agent-gemini, agent-kimi, agent-openrouter, agent-requesty, messaging-discord, messaging-telegram, messaging-whatsapp, ngrok, webhook-ingress, tool-veo, tool-seedance, tool-nanobanana, tool-stability, storage-sss3, messaging-chat, mcp-server, cost-explorer.

## Related

- [Kernel](../kernel.md) — Marketplace API
