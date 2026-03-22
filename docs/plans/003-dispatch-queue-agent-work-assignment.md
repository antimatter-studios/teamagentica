# 001 — Dispatch Queue: Agent Work Assignment via Task Tracker

**Date:** 2026-03-21
**Status:** Draft

## Context

When a task is assigned to an agent (e.g. `@claude`) in the task tracker, we want a controlled, conversational flow:
1. **Assign** → agent reads the task, posts a comment with its proposed plan, then stops
2. **Discuss** → user and agent converse via the comment thread — agent judges intent from natural language and decides whether to answer questions, adjust the plan, start work, or stop
3. **Post-completion** → user can comment on Done/Failed cards, agent responds (can restart if asked)

The cron scheduler becomes the dispatch mechanism — it queues work items and processes them with concurrency control to prevent request spikes.

## Agent Work Flow (state machine)

The scheduler owns the execution loop. Each step is a separate agent dispatch, giving the scheduler control between iterations.

```
Phase 1 — Triage (single dispatch):
  Assign → agent posts proposed plan → move to Todo → STOP

Phase 2 — Execution (scheduler-managed loop):
  User replies → agent judges intent → if approval, responds with {"action":"execute"}:

  For each attempt (max 10):
    1. Scheduler moves card to "In Progress"
    2. Dispatch agent: "implement the fix" (include card + full comment thread)
    3. Agent responds with what it did
    4. Scheduler posts response as comment, moves card to "In Review"
    5. Dispatch agent: "write and run tests to verify the fix"
    6. Agent responds with test results
    7. Scheduler posts results as comment
    8. If tests pass → move to "Done", STOP
    9. If tests fail:
       a. Check for new user comments since last dispatch
       b. If user commented → include in next prompt, agent decides:
          - Stop work → move to "Failed", STOP
          - Replan → move to "Todo", post new plan, STOP (await approval)
          - Continue → incorporate feedback into next retry
       c. If no new comments → retry from step 1

  All 10 retries exhausted → move to "Failed", comment problems + needed fixes, STOP

Phase 3 — The card stays alive:
  Done/Failed is not "closed". The same event mechanism applies forever:
  User comments → task-tracking:comment → scheduler dispatches → agent responds.
  The agent has full context and decides what to do next.
```

**Column requirements for boards using dispatch:**
- Todo, In Progress, In Review, Done, Failed

**"Failed" column** needs to be added to the task-flow and security-review skills too.

## Agent Response Protocol

Agent responses use a structured JSON envelope so the scheduler can parse actions:

```json
{"action": "plan", "message": "Here is my proposed solution..."}
{"action": "execute", "message": "Starting implementation..."}
{"action": "continue", "message": "Test failed because X, retrying with Y approach..."}
{"action": "replan", "message": "Based on your feedback, I need to rethink this..."}
{"action": "stop", "message": "Cannot proceed because X. Needs manual intervention..."}
{"action": "reply", "message": "To answer your question, the reason I chose..."}
{"action": "done", "message": "Solution summary: fixed X by doing Y..."}
{"action": "reopen", "message": "You're right, the fix didn't work because..."}
{"action": "new_task", "message": "The original fix is correct but caused a side-effect...", "new_task_title": "...", "new_task_description": "..."}
```

- `plan` — triage response, scheduler posts comment + moves to Todo
- `execute` — agent signals approval received, scheduler starts execution loop
- `continue` — retry after failure, scheduler moves to In Progress for next attempt
- `replan` — agent wants to redo planning, scheduler moves to Todo
- `stop` — agent gives up, scheduler moves to Failed
- `reply` — conversational response, scheduler posts comment, no state change
- `done` — work complete, scheduler moves to Done
- `reopen` — post-completion: original fix inadequate, scheduler moves card back to In Progress, re-enters execution loop
- `new_task` — post-completion: fix caused a side-effect, scheduler creates a new card on the same board (with title + description from response), posts link to new card as comment on original, new card enters dispatch flow via assign event

The prompt template sent to the agent instructs it to always respond in this format. The agent judges user intent naturally — no magic keywords.

## Prompt Template

The scheduler uses a configurable Go `text/template` prompt. It contains:
- The JSON response protocol (action envelope format)
- The task flow state machine instructions
- Placeholders for dynamic context:
  - `{{.TaskTitle}}`, `{{.TaskDescription}}`, `{{.Priority}}`, `{{.Labels}}`
  - `{{.CurrentColumn}}` — current card state
  - `{{.Comments}}` — full comment thread
  - `{{.TriggerType}}` — "assign" or "comment"
  - `{{.TriggerComment}}` — the user comment that triggered this dispatch
  - `{{.Attempt}}`, `{{.MaxAttempts}}` — retry context
  - `{{.TestResults}}` — previous test results (if retrying)

A sensible default template is embedded in the scheduler code, but users can override via config (`DISPATCH_PROMPT_TEMPLATE`).

## Changes Required

### 1. Task Tracker: Add comment event + single card endpoint
**Files:** `plugins/tool-task-tracker/internal/handlers/handlers.go`, `plugins/tool-task-tracker/main.go`

- Add `GET /cards/:cid` endpoint (calls existing `storage.GetCard()`)
- Emit `task-tracking:comment` event from `CreateComment()` and `MCPAddComment()` with payload: `{card_id, board_id, author_id, body}`
- Only emit if the card has a non-empty `assignee_agent` (so we don't fire events for human-assigned cards)

### 2. Scheduler: DispatchEntry model
**File:** `plugins/infra-cron-scheduler/internal/storage/db.go`

New GORM model:
```
DispatchEntry:
  ID, CardID, BoardID, CardTitle, AgentAlias
  Status: "pending" | "dispatched" | "completed" | "failed"
  DispatchType: "triage" | "reply"
  TriggerComment (the user comment body that triggered this, empty for assign events)
  ErrorMessage, AgentResponse (truncated)
  CreatedAt, DispatchedAt, CompletedAt
```

CRUD methods:
- `CreateDispatchEntry`, `ListDispatchEntries(status, limit)`
- `CountInFlight(agentAlias)`, `CountAllInFlight()`
- `UpdateDispatchStatus(id, status, fields)`
- `ListPendingAgents()` — distinct aliases with pending entries
- `GetNextPending(agentAlias)` — oldest pending for that agent

Register in `Open()` alongside existing models.

### 3. Scheduler: Dispatch config + prompt template
**File:** `plugins/infra-cron-scheduler/plugin.yaml`

Config schema additions:
- `DISPATCH_ENABLED` (boolean, default true)
- `DISPATCH_GLOBAL_LIMIT` (string, empty = unlimited)
- `DISPATCH_AGENT_LIMITS` (string, JSON object e.g. `{"claude":1,"gemini":2}`)
- `DISPATCH_PROMPT_TEMPLATE` (text, multiline) — the master prompt template

### 4. Scheduler: Core dispatch logic
**File:** `plugins/infra-cron-scheduler/internal/scheduler/scheduler.go`

#### Event subscriptions
- `task-tracking:assign` → creates DispatchEntry with type `"triage"`
  - Skip if `assignee_agent` is empty (human assignment)
  - Skip if pending/dispatched entry already exists for same card_id
- `task-tracking:comment` → creates DispatchEntry with type `"reply"`
  - Skip if `author_id` is 0 (system/agent-authored comment — prevents loops)
  - Only process if card has `assignee_agent` set

#### Tick loop integration
Add `processDispatchQueue()` call to existing `tick()`:
- Get distinct agents with pending work
- For each agent: check `canDispatch(agent)` (per-agent limit → global limit → unlimited)
- If allowed: mark as "dispatched", spawn goroutine for `executeDispatch()`

#### Concurrency control: `canDispatch(agentAlias)`
1. If per-agent limit exists for this alias → check `CountInFlight(alias) < limit`
2. Else if global limit > 0 → check `CountAllInFlight() < globalLimit`
3. Else → return true (no limits)

#### Dispatch execution: `executeDispatch(entry)`
Runs in goroutine, 5-minute timeout:

**For type="triage" (triggered by assign):**
1. Fetch full card from task tracker via `RouteToPlugin`
2. Render prompt template with task context + triage instructions
3. Send to relay: `RouteToPlugin("infra-agent-relay", "POST", "/chat", ...)`
4. Parse JSON response, post `message` as comment on card
5. Move card to "Todo" column
6. Mark entry completed, emit `dispatch:completed` event

**For type="reply" (triggered by user comment):**
1. Fetch full card + all comments (conversation history)
2. Determine card's current column (state)
3. Render prompt template with full context + triggering comment
4. Send to relay, parse JSON response
5. Post `message` as comment, execute action (see Agent Response Protocol)

**Execution loop (scheduler-managed, triggered by `"execute"` action):**

```
For attempt = 1 to 10:
  1. Move card to "In Progress"
  2. Dispatch agent: "Implement the solution" (with card + comment thread)
     - Agent responds with what it changed
  3. Post agent response as comment
  4. Move card to "In Review"
  5. Dispatch agent: "Write tests to verify your solution and run them"
     - Agent responds with test results
  6. Post test results as comment
  7. If pass → move to "Done", STOP
  8. If fail:
     - Check for new user comments added since this attempt started
     - Dispatch agent with: failure context + any new user comments
     - Agent responds with decision + reasoning
     - Post as comment
     - If "stop" → move to "Failed", STOP
     - If "replan" → move to "Todo", STOP (awaits new approval)
     - If "continue" → next attempt

  All 10 retries exhausted → move to "Failed", post summary comment, STOP
```

Each dispatch is a separate relay call. The agent finishes its current step before the scheduler checks for new comments — user feedback is incorporated at iteration boundaries, not mid-work.

#### Startup recovery
On init, reset any "dispatched" entries back to "pending" (crashed mid-flight).

### 5. Scheduler: API endpoints
**Files:** `plugins/infra-cron-scheduler/internal/handlers/handlers.go`, `plugins/infra-cron-scheduler/main.go`

REST:
- `GET /dispatch/queue` — list entries, optional `?status=` filter
- `GET /dispatch/queue/:id` — single entry
- `POST /dispatch/queue/:id/retry` — reset failed entry to pending

MCP tools:
- `POST /mcp/list_dispatch_queue`
- `POST /mcp/retry_dispatch`

### 6. Scheduler: main.go wiring
**File:** `plugins/infra-cron-scheduler/main.go`

- Parse dispatch config from `FetchConfig()` result
- Pass `DispatchConfig` to scheduler constructor
- Register new routes on gin router

### 7. Loop prevention
- Agent-posted comments have `author_id = 0` (no X-User-ID header when scheduler calls RouteToPlugin)
- `task-tracking:comment` handler checks: skip if `author_id == 0`
- Duplicate check: skip if pending/dispatched entry exists for same `card_id + agent_alias`

### 8. Add "Failed" column to task-flow and security-review skills
**Files:** `.claude/commands/task-flow.md`, `.claude/commands/security-review.md`

- Add "Failed" as a valid terminal column
- Update task-flow: after 10 retries, move to "Failed" instead of leaving in "In Review"
- Ensure boards created by security-review include a "Failed" column

## Implementation Order

1. Task tracker: `GET /cards/:cid` endpoint
2. Task tracker: `task-tracking:comment` event emission
3. Scheduler: `DispatchEntry` model + storage methods
4. Scheduler: `DispatchConfig` + config parsing in main.go
5. Scheduler: Event handlers (`task-tracking:assign`, `task-tracking:comment`)
6. Scheduler: `processDispatchQueue()` + `canDispatch()` in tick loop
7. Scheduler: `executeDispatch()` — triage + reply flows
8. Scheduler: REST + MCP endpoints for queue visibility
9. Plugin.yaml updates (both plugins)
10. Update task-flow + security-review skills with "Failed" column
11. Build + deploy both plugins

## Verification

1. Create a board with columns: Backlog, Todo, In Progress, In Review, Done, Failed
2. Create a card with title + description
3. Assign card to `@claude`
4. Verify: dispatch queue shows "pending" → "dispatched" → "completed"
5. Verify: comment appears on card with agent's proposed plan, card moved to "Todo"
6. Post a comment approving the plan (e.g. "looks good, let's do it")
7. Verify: agent judges approval, responds with `{"action":"execute"}`, scheduler starts execution loop
8. Verify: card moves In Progress → In Review → Done with comments at each step
9. Post a comment on a Done card: "can you explain why you chose this approach?"
10. Verify: agent responds with `{"action":"reply"}`, comment posted, no state change
11. Test mid-loop feedback: during retry, post a comment, verify agent reads it at next iteration boundary
12. Test replan: post feedback during retry, verify agent responds with `{"action":"replan"}` and card moves to Todo
13. Test concurrency: assign 5 tasks, set global limit to 1, verify sequential processing
14. Test max retries: verify card moves to Failed after 10 failed attempts
