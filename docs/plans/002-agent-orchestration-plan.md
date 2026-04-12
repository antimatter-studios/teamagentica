# Agent Orchestration Architecture Plan

## Overview

Three phases, each building on the last:

| Phase | What | Status |
|-------|------|--------|
| 1 | Relay DAG orchestration engine | In progress |
| 2 | `infra-agent-persona` plugin + alias migration from kernel | Planned |
| 3 | Multi-bot messaging support | Planned |

**Future (not in this plan):** Agent memory — a separate `infra-agent-memory-gateway` plugin. Memory technology will change; keeping it decoupled from identity means either can be upgraded independently. They connect via interface when ready.

---

## Core Architecture

Every layer is dumb about the others. The alias is the only coupling point. Everything is runtime-configurable without code changes or redeployment.

```
[messaging plugins]     I/O adapters         — receive, forward, send back
        ↕
[infra-agent-relay]     orchestration engine — plan, dispatch, collect, respond
        ↕
[infra-agent-persona]   personality layer    — inject system prompt, forward to backend
        ↕
[agent plugins]         API adapters         — call Claude/OpenAI/Gemini, return response
```

---

## Phase 1: Relay DAG Orchestration Engine

### Problem

The relay currently supports only a single `ROUTE:@alias` delegation per request. A coordinator can only hand off to one agent, with no chaining, no parallelism, and no synthesis.

### Solution: Coordinator outputs a JSON DAG

The coordinator receives a request and outputs a full execution plan. The relay validates and executes it — parallel where possible, sequential where dependencies exist.

```json
{
  "tasks": [
    {"id": "t1", "alias": "coder",   "prompt": "find 5 React UI component GitHub projects", "depends_on": []},
    {"id": "t2", "alias": "finance", "prompt": "estimate budget for a React UI project",     "depends_on": []},
    {"id": "t3", "alias": "doc",     "prompt": "write a markdown report:\n\nProjects: {t1}\n\nBudget: {t2}", "depends_on": ["t1", "t2"]}
  ]
}
```

Example parallel execution:
```
@coder ──────────────────┐
                          ▼
                        @doc → final report
                          ▲
@finance ────────────────┘
```

### is_coordinator Flag

Already exists — relay passes `is_coordinator bool` to every agent call:
- `is_coordinator=true` → orchestration mode: "output a JSON task plan, never answer directly"
- `is_coordinator=false` → worker mode: "answer this task directly as a domain expert"

Same model, completely different behaviour — driven entirely by system prompt.

### DAG Rules

- `id` — unique within plan, used for result interpolation
- `alias` — resolved via alias map; `"self"` is reserved (see Synthesis)
- `prompt` — `{tN}` placeholders substituted with actual results before calling
- `depends_on` — tasks with empty list run immediately and in parallel
- Plain coordinator response (no JSON) → returned directly (coordinator answered without delegation)

### Synthesis via "self" Alias

The relay does not decide whether synthesis is needed — the coordinator includes it in the DAG.

`"self"` = call coordinator back in worker mode (`is_coordinator=false`):

```json
{"id": "t4", "alias": "self", "prompt": "combine {t1}, {t2}, {t3} into a final answer", "depends_on": ["t1","t2","t3"]}
```

If no `self` task: terminal task result returned directly.

### Per-Task Timeout

Each task runs with a context deadline (`TASK_TIMEOUT_SECONDS`, default 120s). If exceeded, an error string is injected as the task result so downstream tasks still run.

**Future — streaming heartbeat:** With streaming, each token arrival resets an inactivity timer instead of an absolute deadline. Requires changing agent plugin API from `{Response: string}` to chunked/SSE — planned separately.

### What is Removed

`ROUTE:@alias` format is removed entirely. `ParseCoordinatorResponse()` deleted. Clean break — no backward compat.

### Coordinator System Prompt

Must be updated to output JSON DAG format. Seeded via DB migration (same pattern as kernel migrations) when Phase 2 ships. For Phase 1, update manually in agent config.

The coordinator system prompt instructs:
- Always respond with a JSON task plan
- Use `"self"` alias for synthesis
- Never answer directly (except via a `self` task)
- Available aliases and their capabilities are injected dynamically

### Config Fields Added

| Field | Default | Description |
|-------|---------|-------------|
| `MAX_ORCHESTRATION_TASKS` | 20 | Max tasks in a coordinator plan |
| `TASK_TIMEOUT_SECONDS` | 120 | Per-task deadline in seconds |

### Files Changed

- `plugins/infra-agent-relay/main.go` — DAG executor, `orchestrate()`, `parseCoordinatorPlan()`, `interpolate()`, per-task timeout
- `plugins/infra-agent-relay/plugin.yaml` — new config fields
- `pkg/pluginsdk/alias/coordinator.go` — deleted

### Edge Cases

| Case | Handling |
|------|---------|
| Circular dependency | Detect cycle before execution, return error |
| Unknown alias | Inject error string as result; downstream tasks see it |
| Task fails | Inject error string as result; execution continues |
| Max tasks exceeded | Error before execution starts |
| Plain coordinator response | Return directly |
| Invalid JSON | Fall back to plain response |
| `{tN}` unknown reference | Leave placeholder, log warning |

---

## Phase 2: infra-agent-persona Plugin

### Problem

Alias management lives in the kernel — a microkernel violation. Agent personalities are hardcoded in agent plugin config — not runtime-configurable, not API-manageable.

### Solution: infra-agent-persona owns alias management and persona definitions

A new plugin that:
- Manages a registry of persona definitions (alias + system prompt + backend alias + model)
- Owns the alias system — moves alias management out of the kernel
- Receives requests routed to its aliases, injects the right system prompt, forwards to backend LLM
- Stores personas in a database (not plugin config) — readable and writable at runtime

### Persona Definition

```
alias:         coder
system_prompt: "You are an expert software engineer specialising in..."
backend:       claude          # alias of the LLM plugin to use
model:         claude-opus-4-6 # optional model override
```

### Runtime Flow

```
relay → @coder → infra-agent-persona
                      ↓ injects system_prompt
                      ↓ forwards to @claude
                 agent-anthropic → Claude API → response
```

### Why Database, Not Config

Personas in a database enables programmatic management. An LLM can update a persona's system prompt at runtime:

```
conversation history + [memory store, separate plugin]
    → meta-agent analyzes what worked
    → generates improved system prompt
    → update_persona("coder", new_prompt)
    → @coder now has a better personality
```

### MCP Tools

- `create_persona(alias, system_prompt, backend, model)`
- `update_persona(alias, system_prompt)`
- `get_persona(alias)`
- `list_personas()`

### Kernel Alias Migration

`kernel:alias:update` events and `FetchAliases()` move to infra-agent-persona. Kernel retains only plugin registration. All plugins subscribing to `kernel:alias:update` switch to `persona:alias:update`.

### Agent Plugin Changes

All agent plugins gain a `system_prompt` field in the request body. They use it directly instead of constructing their own. This makes agent plugins pure API adapters.

### Default Personas

Seeded via DB migration — a default general assistant persona out of the box.

### Agent Memory (Future, Separate)

Memory will be `infra-agent-memory-gateway` — a separate plugin. Memory technology will change; keeping it separate from identity means either can be upgraded independently. They connect via interface when ready.

---

## Phase 3: Multi-Bot Messaging Support

### Problem

Each messaging plugin supports one bot token = one identity in Discord/Telegram. Expert personas have no visible presence.

### Solution: Multiple bot tokens per plugin instance

`BOTS_CONFIG` — multiline string, one `alias=token` per line:
```
coder=Bot_TOKEN_AAA
finance=Bot_TOKEN_BBB
infra=Bot_TOKEN_CCC
```

Each bot gets its own goroutine, session, and relay client with unique sourceID (`messaging-discord:coder`). The bot that receives a message IS the routing key — no `@prefix` needed. Backward compat: existing `BOT_TOKEN` + `COORDINATOR_ALIAS` still works.

### Inter-Agent Communication

When a user tells @coder to ask @infra to create a workspace:
1. Message lands on @coder bot → coordinator routes to coder agent
2. Coder agent identifies delegation → relay routes to @infra agent
3. @infra responds; result flows back through coder bot
4. `responder` field attributes the response correctly

---

## Design Principles

- **Relay is mechanical** — executes the DAG, doesn't reason
- **Coordinator is intelligent** — decides the plan, decides synthesis
- **Alias is the universal name** — `@tv`, `@coder`, `@obj` — implementation hidden
- **Every layer is replaceable** — swap model, swap backend, update personality — all at runtime, no code changes
- **Memory is separate** — identity and memory are different concerns, different lifecycles
