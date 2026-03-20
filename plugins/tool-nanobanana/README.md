# tool-nanobanana

AI image generation using Google Gemini's native image output.

## Overview

Wraps the Google Gemini API (`generativelanguage.googleapis.com/v1beta`) to generate images from text prompts. Returns base64-encoded image data synchronously. Also exposes a chat-format endpoint that returns images as markdown-compatible attachments.

## Capabilities

- `agent:tool:image-gen`

## Dependencies

- `cost:tracking`

## Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `GEMINI_API_KEY` | string | yes | — | Google Gemini API key |
| `NANOBANANA_MODEL` | select (dynamic) | no | `gemini-2.5-flash-image` | Model to use for generation |
| `PLUGIN_DEBUG` | boolean | no | `false` | Log request/response traffic |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with config status |
| POST | `/generate` | Generate image from prompt (returns base64 image) |
| POST | `/chat` | Chat-format wrapper around generate (returns attachments) |
| GET | `/tools` | Tool schema for agent discovery |
| GET | `/system-prompt` | System prompt for this tool |
| GET | `/config/options/:field` | Dynamic select options (lists Gemini image models) |
| GET | `/usage` | Accumulated usage summary |
| GET | `/usage/records` | Raw request records, filterable by `?since=` |
| GET | `/pricing` | Get pricing entries |
| PUT | `/pricing` | Update pricing entries |

## Events

- **Emits:** `generate_request`, `generate_complete`, `chat_request`, `chat_response`, `error`
- **Reports usage** to `cost:tracking` via SDK on every generate/chat call

## How It Works

1. Agent or user sends a prompt to `/generate` or `/chat`
2. Plugin calls Gemini `generateContent` with `responseModalities: ["TEXT", "IMAGE"]`
3. Gemini returns inline base64 image data + optional text in the response candidates
4. Plugin extracts image data and returns it directly (synchronous -- no polling)
5. Usage is tracked both locally (file-based) and reported to the cost-tracking system
6. `/chat` wraps the same flow but formats output with `attachments[]` for messaging integration

## Gotchas / Notes

- Image generation is **synchronous** (unlike video tools which are async/polling)
- The `/config/options/NANOBANANA_MODEL` endpoint filters Gemini models to only those containing "image" in the name
- HTTP timeout for generation is 60 seconds
- Pricing is token-based (input/output per 1M) not per-request
