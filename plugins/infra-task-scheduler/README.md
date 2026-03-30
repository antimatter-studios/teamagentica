# infra-task-scheduler

Scheduled task system with two major functions: (1) timer and event-based job scheduling, and (2) a dispatch queue that assigns kanban tasks to AI agents and manages an implement-test-retry execution loop.

## How It Works

### Job Scheduling

Jobs can be triggered two ways:

- **Timer** -- Go duration (`5m`, `1h`) or cron expression (`*/5 * * * *`). A 1-second tick loop checks for due jobs.
- **Event** -- subscribes to SDK event types (e.g. `task-tracking:assign`) and fires matching jobs when events arrive.

Jobs have `once` (fires then disables) or `repeat` (re-arms after firing) behavior. All state is persisted in SQLite.

### Dispatch Queue

When enabled, the dispatch system integrates with `tool-task-tracker` to automate agent work on kanban cards.

**Triggers:**
- `task-tracking:assign` -- creates a "triage" dispatch (agent proposes a plan)
- `task-tracking:comment` -- creates a "reply" dispatch (agent responds to user comment, skips agent/system comments to prevent loops)

**Execution flow:**
1. Agent receives a prompt built from card details, comments, and context
2. Agent responds with structured JSON: `{action, message}`
3. Actions: `plan`, `execute`, `continue`, `replan`, `stop`, `reply`, `done`, `reopen`, `new_task`
4. On `execute`: enters an implement-test-retry loop (up to 10 attempts), moving the card through columns (In Progress -> In Review -> Done/Failed)

**Concurrency control:**
- Global limit across all agents
- Per-agent limits (JSON config, e.g. `{"claude":1,"gemini":2}`)
- Dedup: skips duplicate assign events for same card+agent

Messages are sent to agents via `infra-agent-relay` and card state is managed via `tool-task-tracker`.

### MCP Tools

Exposes 8 tools via MCP: `list_jobs`, `create_job`, `update_job`, `delete_job`, `trigger_job`, `get_log`, `list_dispatch_queue`, `retry_dispatch`.

## Capabilities

- `infra:scheduler`
- `tool:scheduler`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DISPATCH_ENABLED` | boolean | `true` | Process task assignment events and dispatch to agents |
| `DISPATCH_GLOBAL_LIMIT` | string | (empty) | Max concurrent dispatches across all agents (empty = unlimited) |
| `DISPATCH_AGENT_LIMITS` | string | (empty) | JSON: per-agent concurrency limits |
| `DISPATCH_PROMPT_TEMPLATE` | string | (empty) | Override Go text/template for agent prompts |
| `PLUGIN_DEBUG` | boolean | `false` | Debug logging |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/events` | Create job |
| GET | `/events` | List all jobs |
| GET | `/events/:id` | Get single job |
| PUT | `/events/:id` | Update job (partial) |
| DELETE | `/events/:id` | Delete job |
| GET | `/log` | Execution log (supports `?limit=N`, default 50) |
| GET | `/mcp` | MCP tool discovery |
| POST | `/mcp/list_jobs` | MCP: list jobs |
| POST | `/mcp/create_job` | MCP: create job |
| POST | `/mcp/update_job` | MCP: update job |
| POST | `/mcp/delete_job` | MCP: delete job |
| POST | `/mcp/trigger_job` | MCP: manually fire a job |
| POST | `/mcp/get_log` | MCP: get execution log |
| GET | `/dispatch/queue` | List dispatch queue (supports `?status=&limit=`) |
| GET | `/dispatch/queue/:id` | Get dispatch entry |
| POST | `/dispatch/queue/:id/retry` | Retry a failed dispatch |
| POST | `/mcp/list_dispatch_queue` | MCP: list dispatch queue |
| POST | `/mcp/retry_dispatch` | MCP: retry failed dispatch |

## Events

**Subscribes to:**
- `task-tracking:assign` -- creates triage dispatch entries
- `task-tracking:comment` -- creates reply dispatch entries
- Any event patterns configured on event-type jobs

**Emits:**
- `scheduler:fired` -- when a job fires
- `dispatch:completed` -- when a dispatch execution loop finishes successfully

## Notes

- Database: `scheduler.db` in `/data/` (tables: jobs, execution_logs, dispatch_entries).
- Timer tick resolution is 1 second -- jobs can fire up to ~1s late.
- Stale "dispatched" entries from crashes are recovered to "pending" on startup.
- Agent dispatch has a 5-minute timeout per execution step.
- Pushes tool definitions to MCP server on availability.
