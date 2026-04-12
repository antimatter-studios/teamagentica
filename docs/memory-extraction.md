# Memory Extraction in TeamAgentica

## Overview

Memory extraction is the process of analyzing conversation transcripts and documents to extract structured facts, then storing them for later retrieval via semantic search or episodic context assembly.

Two memory plugins handle extraction, each with a different strategy:
- **Mem0** (semantic) — extracts discrete facts, embeds them as vectors, stores in Qdrant
- **LCM** (episodic) — stores full conversation DAGs, summarizes/compacts via LLM when context thresholds are hit

## Two Independent Jobs, Not One

Memory extraction involves **two completely separate tasks** that use different types of models:

### 1. Fact Extraction / Summarization (LLM — chat completions)

**What it does**: Reads conversation text, understands it, extracts structured facts or generates summaries.

**What matters**: Intelligence and reasoning quality. The smarter the model, the better the extraction.

**Any chat model works** — Opus, Gemini Pro, Haiku, GPT-5, Kimi, local Ollama models, anything with `/v1/chat/completions`. This is a standard LLM task. There is no special requirement beyond being a good language model.

### 2. Embedding (Embedding model — embeddings endpoint)

**What it does**: Converts a string into a fixed-size vector (array of floats) for similarity search. This is how "search memories" works — it embeds your query, then finds the closest vectors in the database.

**What matters**: Speed and cost. Embedding is a single forward pass through a small neural network — no token-by-token generation, no reasoning, no chain of thought. It takes milliseconds.

**Requires a dedicated embedding model** — these are tiny, specialized models (e.g., `nomic-embed-text` at ~270MB, `text-embedding-3-small`, `gemini-embedding-001`). Not every provider has one. Not every LLM can do this — it's a fundamentally different operation.

### Why people run embeddings locally

Embedding models are so small and fast that even a laptop CPU handles them fine. This is why Ollama is popular for embeddings — it's free, fast, and the models are trivial to run. The LLM side is where you want your best (usually remote/API) model.

### The two aliases are independent

The Mem0 plugin already has **separate config fields** for each:
- `MEM0_LLM_ALIAS` — for fact extraction (any chat model)
- `MEM0_EMBEDDER_ALIAS` — for embeddings (needs an embedding model)

**These can point to completely different providers.** For example:
- LLM: Opus via agent-anthropic for best extraction quality
- Embedder: Ollama locally with `nomic-embed-text` for free, fast embeddings

Or: LLM via OpenRouter, Embedder via Gemini. Any combination works.

## The `memory:extraction` Capability

Currently, `memory:extraction` is a **static capability** in plugin.yaml that marks a plugin as suitable for memory work. It was originally designed assuming both chat and embeddings come from the same provider.

**This is an oversimplification.** In reality:
- For `LLM_ALIAS`: any plugin with `agent:chat` works (all agent plugins)
- For `EMBEDDER_ALIAS`: only plugins that expose `/v1/embeddings` work (much smaller set)

The capability currently gates both alias dropdowns to the same plugin set, which is more restrictive than necessary. The LLM alias could accept any chat-capable plugin.

### Plugins with `memory:extraction` (current)

| Plugin | Why it qualifies |
|--------|-----------------|
| **agent-google** | Exposes both chat completions and embeddings via OpenAI-compatible proxy |
| **agent-ollama** | Both endpoints natively supported |

### Provider Compatibility Checklist

| Provider | Plugin | Chat Completions | Embeddings | Notes |
|----------|--------|:---:|:---:|-------|
| **Gemini** | agent-google | Yes | Yes | Both via `generativelanguage.googleapis.com/v1beta/openai`. Models: `gemini-embedding-001` for embeddings |
| **Ollama** | agent-ollama | Yes | Yes | Models: `nomic-embed-text`, `mxbai-embed-large`, etc. Free, fast, runs locally |
| **OpenAI** | agent-openai | Yes | Yes | Models: `text-embedding-3-small/large`. Not yet configured/tested in our system |
| **OpenRouter** | agent-openrouter | Yes | **Model-dependent** | Router — has `/api/v1/embeddings` but only for embedding models (e.g., `openai/text-embedding-3-small`). Chat models won't have it |
| **Requesty** | agent-requesty | Yes | **Model-dependent** | Router — same as OpenRouter, `api-v2.requesty.ai/v1/embeddings` available but model-dependent |
| **Moonshot/Kimi** | agent-moonshot | Yes | **No** | No embeddings endpoint in Kimi API. Chat-only |
| **Inception/Mercury** | agent-inception | Yes | **No** | Chat/code/edit only. No embedding models |
| **Claude** | agent-anthropic | Yes (non-OpenAI) | **No** | Anthropic API has no embeddings endpoint |

**Routers (OpenRouter, Requesty)**: Can't statically declare `memory:extraction` because it depends on the user's model selection. A chat model alias won't have embeddings, but an embedding model alias will.

#### Sources
- [Inception API docs — models & endpoints](https://docs.inceptionlabs.ai/get-started/models)
- [Moonshot Kimi API docs](https://platform.moonshot.ai/docs/api/chat)
- [OpenRouter embeddings API](https://openrouter.ai/docs/api/reference/embeddings)
- [Requesty embeddings endpoint](https://docs.requesty.ai/api-reference/endpoint/embeddings-create)

### Future improvement

The `memory:extraction` capability could be split into two separate capabilities:
- `memory:llm` — plugin can do fact extraction/summarization (basically any chat model)
- `memory:embeddings` — plugin exposes `/v1/embeddings` endpoint

This would let the LLM dropdown show all chat-capable plugins while the embedder dropdown only shows embedding-capable ones. Currently both are restricted to `memory:extraction` plugins, which unnecessarily limits LLM choices.

## The @brains Persona

`@brains` was a persona created with role `"memory"` pointing to the `haiku` alias (agent-anthropic, claude-haiku-4-5). It carried a detailed JSON extraction prompt as its system prompt.

**Original confusion**: We thought @brains couldn't work because agent-anthropic lacks `memory:extraction`. But now we understand the LLM and embedder are independent — any chat model can do fact extraction, including Haiku via @brains.

**Current status**: Orphaned — nothing calls it. The extraction prompts live in the memory plugins themselves (Mem0's `custom_fact_extraction_prompt`, LCM's summarization prompts).

**Options**:
1. **Wire @brains into Mem0/LCM** as the extraction LLM, with its prompt managed from the persona dashboard. Gives a single place to tune extraction behavior.
2. **Soft-delete @brains** — the memory plugins own their own prompts and model config already. Simpler, no persona dependency.

## Mem0 — Semantic Memory Extraction

### How it works

1. Agent calls `add_memory` MCP tool with conversation messages
2. Mem0 Go sidecar proxies to the Mem0 Python server (FastAPI)
3. Python server uses configured **LLM** (via `MEM0_LLM_ALIAS`) to extract facts
4. Configured **Embedder** (via `MEM0_EMBEDDER_ALIAS`) generates vectors for each extracted fact
5. Vectors stored in Qdrant (embedded, runs at `/data/qdrant`)
6. Collection names are dimension-keyed (e.g., `memories_1536`) to support model switching

### Extraction prompt (in mem0_server.py)

```
Extract key facts from the conversation. Return valid JSON with a
"facts" key containing a list of plain strings.
Example: {"facts": ["user prefers dark mode", "user is a software engineer"]}
If no facts: {"facts": []}
IMPORTANT: Each fact MUST be a plain string, NOT an object.
Do NOT use keys like content, category, or tags.
Return ONLY the JSON object, no markdown or code blocks.
```

### LLM routing

Mem0's Go sidecar resolves the configured aliases and sets up a local HTTP proxy:

```
Mem0 Python → localhost:8092/memory-api/llm/v1/*      → any chat model (fact extraction)
Mem0 Python → localhost:8092/memory-api/embedder/v1/*  → embedding model (vectorization)
```

These can point to **different providers**. The Python server sees both as OpenAI-compatible endpoints (`provider: "openai"`) with API keys set to `"not-needed"` since auth is handled by the proxy chain.

### Configuration (plugin.yaml)

- `MEM0_LLM_ALIAS` — which alias to use for fact extraction (currently restricted to `memory:extraction` plugins, but any chat model would work)
- `MEM0_EMBEDDER_ALIAS` — which alias to use for embeddings (genuinely needs an embedding-capable plugin)

### MCP tools exposed

| Tool | Purpose |
|------|---------|
| `add_memory` | Extract and store facts from messages |
| `search_memories` | Semantic vector search + optional reranking |
| `get_memories` | List memories with pagination |
| `get_memory` | Retrieve single memory by ID |
| `update_memory` | Modify text/metadata |
| `delete_memory` | Remove single memory |
| `delete_all_memories` | Scope-based bulk deletion |
| `delete_entities` | Hard-delete entities + all their memories |
| `list_entities` | Enumerate users, agents, apps, runs |

## LCM — Episodic Memory (Lossless Context Management)

### How it works

1. Agent calls `store_messages` MCP tool with session_id + messages
2. Go sidecar proxies to TypeScript LCM server (port 8092)
3. Messages stored in immutable SQLite DAG
4. When context exceeds threshold, compaction is triggered
5. LCM calls back to Go sidecar's `/internal/llm/complete` for summarization
6. Go sidecar routes the LLM call to the configured alias
7. Summaries stored in DAG alongside raw messages

**Note**: LCM only needs chat completions (for summarization). It does NOT need embeddings — it uses full-text search, not vector search. So `LCM_LLM_ALIAS` could point to any chat model, not just `memory:extraction` plugins.

### Summarization prompts (in lcm-server/server.ts)

**Normal compaction:**
> "You are a context compaction engine. Summarize the following conversation, preserving key facts, decisions, and context."

**Aggressive compaction:**
> "You are a context compaction engine. Aggressively compress the following into a concise summary preserving only the most critical information."

### Configuration (plugin.yaml)

- `LCM_LLM_ALIAS` — which alias to use for summarization (any chat model works, currently over-restricted to `memory:extraction` plugins)
- `LCM_CONTEXT_THRESHOLD` — fraction of context window that triggers compaction (default: 0.75)
- `LCM_FRESH_TAIL_COUNT` — number of recent messages protected from summarization (default: 32)

### MCP tools exposed

| Tool | Purpose |
|------|---------|
| `store_messages` | Ingest messages into the conversation DAG |
| `get_context` | Assemble context window (summaries + recent messages) |
| `search_messages` | Full-text search across stored messages |
| `expand_summary` | Expand a summary node back to its original messages |

## Architecture Diagram

```
Agent Chat Request
    ↓
Memory Gateway (tool:memory)
    │
    ├── Mem0 (tool:memory:bank:semantic)
    │   ├── Go sidecar (port 8091)
    │   │   └── LLM/Embedder proxy (port 8092)
    │   │       ├── /memory-api/llm/v1/*      → ANY chat model (extraction)
    │   │       └── /memory-api/embedder/v1/*  → embedding model only (vectors)
    │   ├── Python Mem0 server (FastAPI)
    │   └── Qdrant vector store (/data/qdrant)
    │
    └── LCM (tool:memory:bank:episodic)
        ├── Go sidecar (port 8091)
        │   └── /internal/llm/complete → ANY chat model (summarization)
        └── TypeScript LCM server (port 8092)
            └── SQLite DAG store (no embeddings needed)
```

## Key Files

| File | Purpose |
|------|---------|
| `plugins/infra-agent-memory-mem0/main.go` | Mem0 Go sidecar, alias resolution, LLM/embedder proxy |
| `plugins/infra-agent-memory-mem0/mem0_server.py` | Mem0 Python server, extraction config, custom prompt |
| `plugins/infra-agent-memory-lcm/main.go` | LCM Go sidecar, MCP wrapper |
| `plugins/infra-agent-memory-lcm/lcm-server/server.ts` | LCM TypeScript server, DAG, compaction, summarization |
| `plugins/infra-agent-memory-gateway/main.go` | Unified memory router |
| `plugins/agent-google/internal/handlers/openai_proxy.go` | Gemini OpenAI-compatible proxy |
| `plugins/agent-google/plugin.yaml` | Declares `memory:extraction` capability |
| `plugins/agent-ollama/plugin.yaml` | Declares `memory:extraction` capability |
