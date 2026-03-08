# agent-inception

TeamAgentica plugin for [Inception Labs](https://www.inceptionlabs.ai/) — provider of Mercury, the world's first commercial-scale **diffusion large language model** (dLLM).

## Table of Contents

- [Overview](#overview)
- [What is a Diffusion LLM?](#what-is-a-diffusion-llm)
- [Available Models](#available-models)
- [Getting Started](#getting-started)
- [Plugin Configuration](#plugin-configuration)
- [API Endpoints](#api-endpoints)
  - [Chat Completions](#post-chat)
  - [Apply Edit](#post-apply-edit)
  - [Next Edit](#post-next-edit)
  - [FIM (Autocomplete)](#post-fim)
- [Key Features](#key-features)
  - [Tool Use](#tool-use)
  - [Streaming & Diffusing](#streaming--diffusing)
  - [Instant Mode](#instant-mode)
  - [Tunable Reasoning](#tunable-reasoning)
  - [Structured Outputs](#structured-outputs)
- [Pricing](#pricing)
- [Rate Limits](#rate-limits)
- [Benchmarks](#benchmarks)
- [Architecture Notes](#architecture-notes)
- [Environment Variables](#environment-variables)
- [Development](#development)

---

## Overview

Mercury 2 is the **fastest reasoning LLM** available today, achieving ~1,000 tokens/second output throughput — roughly **10x faster** than comparable autoregressive models like Claude 4.5 Haiku (~89 tok/s) or GPT-5 Mini (~71 tok/s). It accomplishes this through a fundamentally different architecture: instead of predicting tokens one-at-a-time, Mercury generates all tokens in parallel and refines them through iterative denoising.

This plugin integrates Mercury models into TeamAgentica, providing:
- **Chat completions** with tool use and structured outputs
- **Apply-edit** for intelligent code merging
- **Next-edit** for predictive code editing (IDE-style)
- **FIM (fill-in-the-middle)** for autocomplete
- Full usage tracking and cost reporting

## What is a Diffusion LLM?

Traditional LLMs (GPT, Claude, Gemini) are **autoregressive** — they generate one token at a time, left to right. This creates a fundamental speed bottleneck: each token must wait for the previous one.

Mercury uses **diffusion** — the same paradigm that powers image generation models like Stable Diffusion and DALL-E. Here's how it works:

1. **Start with noise**: The model begins with a rough sketch of the full output (random/noisy tokens)
2. **Iterative denoising**: Each pass through the model refines multiple tokens simultaneously
3. **Parallel generation**: A single neural network evaluation produces far more useful work per step
4. **Convergence**: After several denoising passes, the output converges to coherent text

Key technical details:
- Uses a **masking-based corruption process** designed for discrete tokens (not Gaussian noise like image diffusion)
- Parameterized via the **Transformer architecture**
- Trained to **predict multiple tokens in parallel**
- Noisy tokens shown during the diffusion visualisation are **not counted for billing**

This approach trades the sequential bottleneck of autoregressive models for parallel computation, making it dramatically faster while maintaining competitive quality.

## Available Models

| Model | Type | Context Window | Best For |
|-------|------|---------------|----------|
| `mercury-2` | Reasoning dLLM | 128K tokens | General chat, reasoning, agentic workflows, tool use |
| `mercury-coder-small` | Code dLLM | 128K tokens | Code generation, fast coding tasks |
| `mercury-edit` | Edit model | — | Apply-edit, next-edit, FIM autocomplete |

### Model Selection Guide

- **`mercury-2`** — Default. Best overall model with reasoning capabilities. Use for chat, agents, structured outputs, and tool-calling workflows. Supports tunable `reasoning_effort` from "instant" to "high".
- **`mercury-coder-small`** — Specialised for code. >5x faster than speed-optimised frontier coding models. Ideal for high-volume code generation where raw speed matters.
- **`mercury-edit`** — Specialised model for code editing operations. Used automatically by the apply-edit, next-edit, and FIM endpoints.

## Getting Started

1. **Get an API key** at [platform.inceptionlabs.ai](https://platform.inceptionlabs.ai/)
2. **Configure the plugin** in the TeamAgentica UI:
   - Navigate to the plugin settings
   - Enter your `INCEPTION_API_KEY`
   - Select your preferred model (default: `mercury-2`)
3. **Start chatting** — the plugin registers with capability `ai:chat:inception`

### Quick Test (curl)

```bash
export INCEPTION_API_KEY="your_api_key_here"

curl https://api.inceptionlabs.ai/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $INCEPTION_API_KEY" \
  -d '{
    "model": "mercury-2",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "What is a diffusion model?"}
    ],
    "max_tokens": 1000
  }'
```

## Plugin Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `INCEPTION_API_KEY` | string (secret) | — | Your Inception Labs API key (required) |
| `INCEPTION_MODEL` | select (dynamic) | `mercury-2` | Default model for chat completions |
| `INCEPTION_INSTANT` | boolean | `false` | Enable instant mode globally (lowest latency) |
| `INCEPTION_DIFFUSING` | boolean | `false` | Enable diffusion visualisation globally |
| `PLUGIN_ALIASES` | aliases | — | Routing aliases for this plugin |
| `PLUGIN_DEBUG` | boolean | `false` | Log detailed request/response traffic |

## API Endpoints

### `GET /health`

Health check. Returns `configured: true` when API key is set.

### `POST /chat`

Standard chat completion endpoint. OpenAI-compatible request/response format.

**Request:**
```json
{
  "message": "What is a diffusion model?",
  "model": "mercury-2",
  "conversation": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "What is a diffusion model?"}
  ],
  "reasoning_effort": "high",
  "diffusing": true
}
```

- `message` — Simple single-turn message (use this OR `conversation`)
- `model` — Optional per-request model override
- `conversation` — Multi-turn message array (roles: `system`, `user`, `assistant`, `tool`)
- `reasoning_effort` — Optional: `"instant"`, `"low"`, `"medium"`, `"high"` (overrides global instant setting)
- `diffusing` — Optional: override global diffusing setting per-request

**Response:**
```json
{
  "response": "A diffusion model is...",
  "model": "mercury-2",
  "usage": {
    "prompt_tokens": 25,
    "completion_tokens": 150
  }
}
```

### `POST /apply-edit`

Intelligently merges a code update snippet into original code, preserving structure, order, comments, and indentation. Uses the `mercury-edit` model via the `/v1/apply/completions` endpoint.

**Request:**
```json
{
  "original_code": "class Calculator:\n    def __init__(self):\n        self.history = []\n\n    def add(self, a, b):\n        result = a + b\n        return result",
  "update_snippet": "// ... existing code ...\ndef multiply(self, a, b):\n    result = a * b\n    return result\n// ... existing code ..."
}
```

**Response:**
```json
{
  "response": "class Calculator:\n    def __init__(self):\n        self.history = []\n\n    def add(self, a, b):\n        result = a + b\n        return result\n\n    def multiply(self, a, b):\n        result = a * b\n        return result",
  "model": "mercury-edit",
  "usage": { "prompt_tokens": 80, "completion_tokens": 120 }
}
```

**How it works:**
- The original code is wrapped in `<|original_code|>...<|/original_code|>` tags
- The update snippet is wrapped in `<|update_snippet|>...<|/update_snippet|>` tags
- The model intelligently merges them, using `// ... existing code ...` markers to indicate preserved regions
- The response contains the complete merged code

### `POST /next-edit`

Predicts the next code edit based on cursor position, recent edits, and file context. Uses the `mercury-edit` model via the `/v1/edit/completions` endpoint.

**Request:**
```json
{
  "recent_snippets": "",
  "current_file_content": "current_file_path: solver.py\n'''''''''\nfunction: flagAllNeighbors\n----------\nThis function marks each of the covered neighbors...\n<|code_to_edit|>\ndef flagAllNeighbors(board<|cursor|>, row, col):\n    for r, c in b.getNeighbors(row, col):\n        if b.isValid(r, c):\n            b.flag(r, c)\n\n<|/code_to_edit|>",
  "edit_diff_history": "--- solver.py\n+++ solver.py\n@@ -6,1 +6,1 @@\n-def flagAllNeighbors(b, row, col):\n+def flagAllNeighbors(board, row, col):"
}
```

**Key concepts:**
- `<|cursor|>` — Marks current cursor position in the code
- `<|code_to_edit|>...<|/code_to_edit|>` — Marks the editable region
- `<|edit_diff_history|>` — Recent edits in unidiff format (most recent at bottom)
- `<|recently_viewed_code_snippets|>` — Other code the user has viewed for context
- Mercury returns an updated version of the editable region

### `POST /fim`

Fill-in-the-middle autocomplete. Generates code that fits between a prompt (prefix) and suffix. Uses the `mercury-edit` model via the `/v1/fim/completions` endpoint.

**Request:**
```json
{
  "prompt": "def fibonacci(",
  "suffix": "return a + b"
}
```

**Response:**
```json
{
  "response": "n):\n    if n <= 1:\n        return n\n    a, b = 0, 1\n    for _ in range(2, n + 1):\n        a, b = b, a + b\n    ",
  "model": "mercury-edit",
  "usage": { "prompt_tokens": 12, "completion_tokens": 45 }
}
```

### `GET /models`

Returns available models and current default.

### `GET /usage`

Returns aggregated usage stats (today, this week, all-time).

### `GET /usage/records?since=<RFC3339>`

Returns raw request-level usage records, optionally filtered.

### `GET /pricing` / `PUT /pricing`

View or update pricing data for cost tracking.

## Key Features

### Tool Use

Mercury 2 supports OpenAI-compatible function calling. This plugin automatically discovers tool plugins registered with the kernel (capability prefix `tool:`) and exposes them to Mercury.

**How it works:**
1. Plugin discovers `tool:*` plugins from the kernel
2. Fetches tool schemas from each plugin's `GET /tools` endpoint
3. Converts to OpenAI-compatible function definitions
4. Sends tools with each chat request
5. When Mercury requests a tool call, the plugin executes it via the kernel proxy
6. Tool results are fed back to Mercury for the next iteration
7. Up to 5 tool-use iterations per request

Mercury supports `tool_choice: "auto"` and generates tool calls in the standard OpenAI format:
```json
{
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get current weather",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {"type": "string"}
        },
        "required": ["location"]
      }
    }
  }]
}
```

### Streaming & Diffusing

Mercury supports two streaming modes:

#### Standard Streaming (`stream: true`)
- Server-sent events (SSE) format
- Responses arrive block-by-block via `delta.content`
- Uses `data: [DONE]` sentinel
- Ideal for chat UIs with real-time feedback

#### Diffusion Streaming (`diffusing: true`)
- Automatically enables streaming
- Instead of appending deltas, the model sends **full content snapshots** that refine over time
- Each `delta.content` **replaces** the current buffer (not appends)
- Visualises the denoising process: you see noisy tokens gradually converge to coherent text
- **Noisy tokens are not billed** — only the final output counts
- Fascinating to watch and great for demos

**JavaScript example for diffusing mode:**
```javascript
const reader = response.body.getReader();
const decoder = new TextDecoder();
let fullContent = '';

while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    const chunk = decoder.decode(value);
    const lines = chunk.split('\n');

    for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed || !trimmed.startsWith('data: ')) continue;
        if (trimmed === 'data: [DONE]') continue;
        const jsonStr = trimmed.substring(6);
        if (!jsonStr.startsWith('{')) continue;
        try {
            const data = JSON.parse(jsonStr);
            for (const choice of data.choices || []) {
                if (choice.delta && choice.delta.content != null) {
                    // NOTE: In diffusing mode, REPLACE content, don't append
                    fullContent = choice.delta.content || '';
                    contentElement.textContent = fullContent;
                }
            }
        } catch (error) {
            console.error('Parsing error:', error);
        }
    }
}
```

> **Note:** This plugin currently proxies non-streaming requests. Streaming support through the plugin is a future enhancement — for now, streaming can be used by calling the Inception API directly.

### Instant Mode

Set `reasoning_effort: "instant"` for near-zero-latency responses. Ideal for:
- Voice assistants
- Customer support chatbots
- Real-time decision systems
- Low-latency automation workflows
- High-throughput batch processing

Can be enabled globally via the `INCEPTION_INSTANT` config option, or per-request via the `reasoning_effort` field.

### Tunable Reasoning

Mercury 2 supports a `reasoning_effort` parameter that controls the depth of reasoning:

| Value | Speed | Quality | Use Case |
|-------|-------|---------|----------|
| `instant` | Fastest | Good | Real-time chat, voice, autocomplete |
| `low` | Very fast | Better | Simple Q&A, classification |
| `medium` | Fast | Good | General-purpose tasks |
| `high` | Standard | Best | Complex reasoning, math, code |

This allows you to tune the speed-quality trade-off on a per-call basis depending on the task. For example, in an agentic pipeline:
- Use `instant` for tool-argument generation and structured state passing
- Use `medium` for planning and query decomposition
- Use `high` for final synthesis and complex reasoning steps

### Structured Outputs

Mercury 2 supports JSON schema-constrained outputs via the `response_format` parameter:

```python
response_format = {
    "type": "json_schema",
    "json_schema": {
        "name": "Sentiment",
        "strict": True,
        "schema": {
            "type": "object",
            "properties": {
                "sentiment": {
                    "type": "string",
                    "enum": ["positive", "negative", "neutral"]
                },
                "confidence": {
                    "type": "number",
                    "minimum": 0,
                    "maximum": 1
                },
                "key_phrases": {
                    "type": "array",
                    "items": {"type": "string"}
                }
            },
            "required": ["sentiment", "confidence", "key_phrases"]
        }
    }
}
```

This ensures deterministic output formatting, which is critical for:
- Query planning in agent pipelines
- Tool argument generation
- Structured state passing between agent steps
- Data extraction and classification
- Verification pipelines

## Pricing

| Model | Input (per 1M tokens) | Output (per 1M tokens) |
|-------|----------------------|------------------------|
| `mercury-2` | $0.25 | $0.75 |
| `mercury-coder-small` | $0.25 | $0.75 |
| `mercury-edit` | $0.25 | $0.75 |

**Cost comparison** (approximate, per 1M output tokens):
- Mercury 2: **$0.75**
- GPT-4o: $10.00
- Claude 4.5 Haiku: ~$5.00
- GPT-4o-mini: $0.60

Mercury 2 is extremely cost-competitive, especially considering its ~10x throughput advantage.

## Rate Limits

| Tier | API Requests/min | Input Tokens/min | Output Tokens/min |
|------|-----------------|-------------------|-------------------|
| **Free** | 100 | 100,000 | 10,000 |
| **Pay As You Go** | 1,000 | 1,000,000 | 100,000 |
| **Enterprise** | 10,000+ | 10,000,000+ | 1,000,000+ |

Rate-limited requests return HTTP 429 with a `retry_after` field. The API also returns HTTP 503 when engines are overloaded.

## Benchmarks

Mercury 2 benchmark scores (as reported by Inception Labs and third-party evaluations):

| Benchmark | Mercury 2 | Notes |
|-----------|-----------|-------|
| AIME 2025 | 91.1 | Math reasoning |
| GPQA Diamond | 73.6 | Graduate-level science Q&A |
| IFBench | 71.3 | Instruction following |
| LiveCodeBench | 67.3 | Live coding tasks |
| SciCode | 38.4 | Scientific coding |
| Tau2 | 52.9 | Complex reasoning |

**Speed benchmarks:**
- Mercury 2: **~1,000 tok/s** output
- Claude 4.5 Haiku: ~89 tok/s
- GPT-5 Mini: ~71 tok/s
- Time-to-first-token: ~3.8 seconds (includes reasoning)

Quality is within **5–15%** of frontier autoregressive models on complex reasoning, while **matching or exceeding** on structured output and translation tasks. The massive speed advantage makes it ideal for latency-sensitive production workloads.

## Architecture Notes

### Why Diffusion for Text?

The autoregressive paradigm has an inherent bottleneck: generating N tokens requires N sequential forward passes. Techniques like speculative decoding and KV-cache optimisation help, but can't overcome the fundamental sequential nature.

Diffusion models sidestep this entirely:
- Each forward pass refines **all tokens simultaneously**
- The number of denoising steps (typically 5–20) is independent of output length
- This means generating 100 tokens and 1,000 tokens takes roughly the same number of forward passes
- The result is output throughput that scales much better with sequence length

### Trade-offs

- **Latency vs quality**: The `reasoning_effort` parameter controls how many denoising passes to run. More passes = better quality but higher latency.
- **Time-to-first-token**: Because the model generates all tokens in parallel, there's no "streaming start" — the first token arrives only after the first denoising pass is complete (~3.8s). This is higher than autoregressive models but the total generation time is much lower.
- **Verbosity**: Mercury 2 tends to be verbose (generated 69M tokens vs 20M median in evaluations). This is worth considering for cost estimates.

### Agentic Workflows

Mercury 2 is particularly well-suited for agent loops because:
1. **Speed**: Each agent step (tool selection, argument generation, result synthesis) completes in milliseconds
2. **Tool use**: Native function calling support in OpenAI-compatible format
3. **Structured outputs**: JSON schema enforcement for reliable state passing between steps
4. **Tunable reasoning**: Use `instant` for fast iterations, `high` for critical decisions
5. **Cost**: At $0.75/M output tokens, running many agent iterations is affordable

### Unique Capabilities Not Found in Other Providers

1. **Diffusion visualisation** (`diffusing: true`): Watch the model "think" as noisy tokens converge to coherent text. No other provider offers this.
2. **Apply-edit** (`/v1/apply/completions`): Purpose-built endpoint for merging code changes with structure preservation. Uses special `<|original_code|>` and `<|update_snippet|>` markup.
3. **Next-edit** (`/v1/edit/completions`): IDE-style predictive editing that considers cursor position, recent edits, and file context. Uses `<|cursor|>`, `<|code_to_edit|>`, and `<|edit_diff_history|>` markup.
4. **FIM** (`/v1/fim/completions`): Fill-in-the-middle autocomplete with explicit prompt/suffix separation.
5. **Parallel token generation**: Fundamentally different architecture from all other providers.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `INCEPTION_API_KEY` | — | Inception Labs API key (required) |
| `INCEPTION_MODEL` | `mercury-2` | Default model |
| `INCEPTION_API_ENDPOINT` | `https://api.inceptionlabs.ai/v1` | API base URL |
| `INCEPTION_INSTANT` | `false` | Enable instant mode globally |
| `INCEPTION_DIFFUSING` | `false` | Enable diffusing mode globally |
| `INCEPTION_DATA_PATH` | `/data` | Path for usage data persistence |
| `AGENT_INCEPTION_PORT` | `8085` | HTTP server port |
| `PLUGIN_DEBUG` | `false` | Enable debug logging |
| `TEAMAGENTICA_KERNEL_HOST` | `localhost` | Kernel hostname |
| `TEAMAGENTICA_KERNEL_PORT` | `8080` | Kernel port |
| `TEAMAGENTICA_PLUGIN_ID` | `agent-inception` | Plugin ID for kernel registration |
| `TEAMAGENTICA_PLUGIN_TOKEN` | — | Plugin auth token |

## Development

### Local Development

```bash
cd plugins/agent-inception
task dev
```

### Build

```bash
task build
```

### Docker

```bash
# Dev with hot reload
docker build --target dev -t agent-inception:dev .

# Production
docker build --target prod -t agent-inception:prod .
```

### Testing

```bash
task test
```

### Project Structure

```
agent-inception/
├── main.go                         # Entry point, routes, SDK registration
├── go.mod / go.sum                 # Dependencies
├── Dockerfile                      # Multi-stage: dev, builder, prod
├── .air.toml                       # Hot-reload config
├── Taskfile.yml                    # Build tasks
├── README.md                       # This file
└── internal/
    ├── config/
    │   └── config.go               # Environment variable loading
    ├── handlers/
    │   ├── chat.go                 # All HTTP handlers (chat, apply-edit, next-edit, FIM, usage)
    │   └── tools.go                # Tool discovery, execution, media extraction
    ├── inception/
    │   └── client.go               # Inception API client (chat, apply-edit, next-edit, FIM)
    └── usage/
        └── tracker.go              # Usage persistence and aggregation
```

## Further Reading

- [Inception Labs Homepage](https://www.inceptionlabs.ai/)
- [Inception API Documentation](https://docs.inceptionlabs.ai/get-started/get-started)
- [Mercury 2 Announcement](https://www.inceptionlabs.ai/blog/introducing-mercury-2)
- [Mercury Architecture Paper](https://arxiv.org/html/2506.17298v1)
- [Apply-Edit Blog Post](https://www.inceptionlabs.ai/blog/ultra-fast-apply-edit-with-mercury-coder)
- [Next-Edit Blog Post](https://www.inceptionlabs.ai/blog/next-edit-continue)
- [Building a Research Agent with Mercury 2 (DataCamp)](https://www.datacamp.com/tutorial/mercury-2-tutorial)
- [Mercury 2 Performance Analysis (Artificial Analysis)](https://artificialanalysis.ai/models/mercury-2)
- [Python SDK (unofficial)](https://github.com/hamzaamjad/mercury-client)
- [OpenRouter Provider](https://openrouter.ai/inception/mercury)
