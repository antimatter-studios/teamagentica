Task tracker operations: list, create, update, comment, and manage task cards on kanban boards.

Arguments: $ARGUMENTS

The arguments describe what you want to do. Examples:
- "list boards"
- "list tasks on SDK Refactor board"
- "create a bug on Infra board: redis connection timeout"
- "move INFRA-12 to done"
- "what's the status of INFRA-12?"
- "list epics on SDK Refactor board"
- "show all urgent tasks"

## Authentication

Get a JWT token by running:
```bash
task kernel:get_token
```

Store it for subsequent requests:
```bash
TOKEN=$(task kernel:get_token 2>/dev/null)
```

If the token doesn't work (e.g. expired or invalid), reconnect first:
```bash
task kernel:connect
```
Then re-run `task kernel:get_token` to get a fresh token.

## API Base URL

```
TRACKER="http://api.teamagentica.localhost/api/route/tool-task-tracker"
```

All endpoints below are relative to `$TRACKER`. Every request must include:
```
Authorization: Bearer $TOKEN
Content-Type: application/json
```

## API Reference

### Boards

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /boards | List all boards |
| POST | /boards | Create board (name required) |
| GET | /boards/:id | Get board |
| PUT | /boards/:id | Update board (partial) |
| DELETE | /boards/:id | Delete board (cascades) |

### Columns (Statuses)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /boards/:id/columns | List columns for board |
| POST | /boards/:id/columns | Create column (name required) |
| PUT | /boards/:id/columns/:cid | Update column (partial) |
| DELETE | /boards/:id/columns/:cid | Delete column |

### Epics

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /boards/:id/epics | List epics for board |
| POST | /boards/:id/epics | Create epic (name required) |
| PUT | /boards/:id/epics/:eid | Update epic (partial) |
| DELETE | /boards/:id/epics/:eid | Delete epic (unlinks cards) |

**Discovering fields for any resource:** GET an existing instance first and inspect its JSON keys. Use those same keys when creating or updating. All PUT endpoints accept partial bodies.

### Cards (Tasks)

| Method | Endpoint | Body | Description |
|--------|----------|------|-------------|
| GET | /boards/:id/cards | - | List all cards (enriched with status_name, assignee_name) |
| POST | /boards/:id/cards | JSON (see discovery below) | Create card (column_id and title required) |
| GET | /boards/:id/cards/search?q=term | - | Search cards (substring on title, description, labels) |
| GET | /boards/:id/cards/number/:num | - | Get card by board-scoped number |
| GET | /cards/:cid | - | Get card by UUID |
| PUT | /boards/:id/cards/:cid | JSON (partial, see discovery below) | Update card |
| DELETE | /boards/:id/cards/:cid | - | Delete card |

**Discovering card fields:** Do NOT hardcode card schemas. Instead, GET an existing card from the board and inspect its JSON fields. Use those same field names when creating or updating cards. The only required fields for creation are `column_id` and `title`. For updates, all fields are optional — only send what you want to change. To clear optional fields (epic, assignee, due_date), use the corresponding `clear_epic`, `clear_assignee`, `clear_due` boolean flags.

### Comments

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /cards/:cid/comments | List comments (enriched with author_name) |
| POST | /cards/:cid/comments | Add comment (body required) |
| DELETE | /cards/:cid/comments/:cmid | Delete comment |

### MCP Tool Endpoints (POST, JSON body)

These are agent-callable tool endpoints. Discover required fields by calling each endpoint with `{}` and reading error messages, or by inspecting existing data from GET endpoints.

| Endpoint | Description |
|----------|-------------|
| /mcp/list_boards | List boards with columns |
| /mcp/list_epics | List epics for a board |
| /mcp/create_epic | Create epic |
| /mcp/update_epic | Update epic |
| /mcp/delete_epic | Delete epic |
| /mcp/list_tasks | List tasks grouped by column |
| /mcp/list_tasks_by_status | List tasks in one column |
| /mcp/create_task | Create task |
| /mcp/set_task_state | Move task to column |
| /mcp/update_task | Update task fields |
| /mcp/search_tasks | Search tasks |
| /mcp/add_comment | Add comment |

## Board Column Convention

Standard kanban columns (by position):
1. **Todo** (position 0) — not yet started
2. **In Progress** (position 1) — actively being worked on
3. **In Review** (position 2) — fix applied, needs verification
4. **Done** (position 3) — verified complete
5. **Failed** (position 4) — gave up after max attempts

## Common Operations

All curl commands assume `$TOKEN` and `$TRACKER` are set (see Authentication and API Base URL above).

- **List boards:** `GET /boards`
- **List columns:** `GET /boards/$BOARD_ID/columns`
- **List epics:** `GET /boards/$BOARD_ID/epics`
- **List all tasks:** `GET /boards/$BOARD_ID/cards`
- **List tasks by state:** `POST /mcp/list_tasks_by_status` with column_id
- **Search tasks:** `GET /boards/$BOARD_ID/cards/search?q=<term>`

## Creating a Task

1. **Resolve the board** — GET /boards, find by name
2. **Get columns** — GET /boards/:id/columns, find the Todo column ID
3. **Discover fields** — GET an existing card from the board to see available fields and their formats
4. **Create the card** — POST /boards/:id/cards with at minimum `column_id` and `title`, plus any other fields you discovered
5. **Add an initial comment** explaining context or acceptance criteria

## Updating a Task

- **Move column:** PUT /boards/:id/cards/:cid with the target column_id
- **Add comment:** POST /cards/:cid/comments with comment body
- **Update fields:** PUT /boards/:id/cards/:cid with any fields to change (partial update)

Discover available fields by GETting the card first and inspecting its JSON.

## Checking Task Status (Read-Only)

When the user just wants a status update (not to do work):

1. Find the card via boards + cards or search
2. Read the card's `status_name`, `priority`, `labels`, `updated_at`
3. Read the card's comments for recent activity
4. Report concisely:
```
Task: INFRA-12 — Fix redis connection timeout
Status: In Review
Priority: high
Labels: infrastructure, redis
Last update: [timestamp] — [latest comment summary]
```

Do NOT move cards or do work unless explicitly asked.

## Labels with Special Meaning

### `accepted`
A human has reviewed this task and approved it for agent work. Agents should prioritize tasks with this label. Only work on tasks that have been accepted unless explicitly told otherwise.

### `rejected`
A human has reviewed this task and does NOT approve it. Do NOT work on rejected tasks. If asked to work on a rejected task, inform the user it has been rejected and ask for confirmation before proceeding.

## Task Lifecycle (Agent Processing Flow)

When an agent is assigned to process a task from Todo to Done:

### 1. Pick up the task
- Read the card title, description, labels, and comments
- Check for the `accepted` label — if absent, ask the user before starting
- Check for the `rejected` label — if present, skip and report
- Move to **In Progress**
- Comment: "Starting work — [brief plan]"

### 2. Do the work
- Implement the fix/feature described in the card
- Track attempt count (start at 1)

### 3. Submit for review
- Move to **In Review**
- Comment: "Attempt [N] — [what was done, files changed, how to verify]"

### 4. Verify
- Run tests, check the implementation, validate the fix
- If verification passes: move to **Done**, comment: "Verified — [brief confirmation]"
- If verification fails: proceed to retry flow

### 5. Retry on failure
- Move back to **In Progress**
- Comment: "Review failed — [what went wrong]. Retrying (attempt [N+1])"
- Fix the issue and go back to step 3
- **Maximum 10 attempts** — if all 10 fail, move to **Failed**

### 6. Giving up (Failed)
After 10 failed attempts:
- Move to **Failed**
- Comment: "Gave up after 10 attempts. Last failure: [description]. Needs human intervention."
- Do NOT keep retrying — leave it for a human to investigate

## Comment Discipline

**Every card movement MUST have a comment explaining why.** Comments are the audit trail. Without them, card movements are meaningless.

Required comments:
- Moving to In Progress: what you plan to do
- Moving to In Review: what you did, how to verify
- Moving to Done: confirmation it's verified
- Moving to Failed: why it can't be fixed, what was tried
- Moving back from In Review: what failed in review
- Any label or priority change: why

## Important Rules

- **Never delete cards** — move to Done or Failed instead
- **Always comment before moving** — comments are the audit trail
- **Respect labels** — `accepted` means go, `rejected` means stop
- **Max 10 attempts** — then move to Failed, don't loop forever
- **Soft deletes** — the system uses soft deletes, cards are never truly gone
- **Card numbers** — cards have board-scoped numbers (e.g. INFRA-12), use these when referencing cards to users
- **Partial updates** — PUT endpoints accept partial bodies, only send fields you want to change
- **Clearing fields** — to unset optional fields, look for `clear_*` boolean flags on the card (discover by inspecting card JSON)

## Report Format

After any operation, print a concise summary:
```
| Card | Status | Priority | Action |
|------|--------|----------|--------|
| INFRA-12 | Done | high | Closed — redis timeout fixed |
```
