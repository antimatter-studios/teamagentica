Manage the physical flow of task card(s) through a task tracker board.

Arguments: $ARGUMENTS

The arguments can be:
- A board name or ID + card title(s) to work on
- "all backlog" to work on all Todo items in a board
- A description of new task(s) to create and work on

## Setup

Resolve the task tracker API:
```
TRACKER="http://api.teamagentica.localhost/api/route/tool-task-tracker"
TOKEN from ~/.tacli/config.json profiles[0].token
```

## Board Column Convention

Boards follow this column structure:
1. **Todo** (position 0) — not yet started
2. **In Progress** (position 1) — actively being worked on
3. **In Review** (position 2) — fix applied, needs verification
4. **Done** (position 3) — verified complete
5. **Failed** (position 4) — gave up after max attempts

## Card Operations

### Resolve or create a card
- If a card ID or title is given, find it via `GET /boards` then `GET /boards/:id/cards`
- If creating a new task, POST to `/boards/:id/cards` with title, description, priority, labels
- Record the card_id, board_id, and column IDs

### Move a card
PUT the card with `column_id` set to the target column.

### Read comments
```
GET /cards/:card_id/comments
```
Look for:
- Questions from other users that challenge the approach
- Alternative suggestions
- Constraints or caveats
- Reports that the issue is a false positive or by design

### Add a comment
POST to `/cards/:card_id/comments` with the update. Always add comments — this is the audit trail.

### Retry flow
- Track attempt count per card (max 10)
- On success: move to Done
- On failure: add comment with attempt details, stay In Progress for retry
- On max attempts: add comment "Gave up after 10 attempts", move to Failed

## Report

After all cards are processed, print a summary table:

```
| Task | Priority | Status | Attempts | Notes |
|------|----------|--------|----------|-------|
```
