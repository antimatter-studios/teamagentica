Evaluate a task card on a task tracker board and update it with findings.

Arguments: $ARGUMENTS

The arguments should include:
- A board name (or partial match) and task title (or partial match)
- Optionally: specific questions or context about the task

## Setup

Resolve the task tracker API:
```
TRACKER="http://api.teamagentica.localhost/api/route/tool-task-tracker"
TOKEN from ~/.config/teamagentica/tacli.json profiles[0].token
```

## Workflow

### 1. Find the board and card
- GET /boards — find the board by partial name match
- GET /boards/:id/cards — find the card by partial title match
- GET /boards/:id/columns — map column IDs to names
- GET /cards/:card_id/comments — read existing comments for context

### 2. Understand the task
Read the card title, description, labels, and any existing comments. Understand what work the task is asking for.

### 3. Evaluate current state
Research the codebase to determine whether this work has been done, partially done, or not started:
- Search for relevant code, functions, files mentioned in the task
- Check git log for related commits if helpful
- Look at the actual implementation to verify completeness

### 4. Determine outcome

**If the task is ALREADY COMPLETE:**
- Add a detailed comment explaining:
  - Where the implementation lives (file paths, line numbers)
  - How it satisfies the task requirements
  - Any caveats or gaps (if minor and acceptable, still close)
- Move the card to Done column
- Report: "Closed — [one-line reason]"

**If the task is PARTIALLY DONE:**
- Add a comment explaining:
  - What has been done and where
  - What remains to be done
  - Whether the scope has changed from the original description
- Update the card description if scope has materially changed
- Keep the card in its current column (or move to In Progress if in Todo)
- Ask the user: "Want me to work on the remaining items?"

**If the task is NOT STARTED:**
- Add a comment with your assessment:
  - Is the task still relevant?
  - Has the architecture changed such that the approach needs updating?
  - Estimated complexity (small/medium/large)
- Update description if the original is stale or vague
- Ask the user: "Want me to work on this?"

**If the task is NO LONGER RELEVANT:**
- Add a comment explaining why (e.g., feature removed, approach changed, superseded by other work)
- Move the card to Done column
- Report: "Closed as obsolete — [one-line reason]"

### 5. Report

Print a concise summary:
```
Task: [title]
Board: [board name]
Status: [Complete | Partial | Not Started | Obsolete]
Action: [what you did — commented, closed, updated scope, etc.]
```

## Board Column Convention

Standard columns (by position):
1. **Todo** (position 0) — not yet started
2. **In Progress** (position 1) — actively being worked on
3. **In Review** (position 2) — needs verification
4. **Done** (position 3) — verified complete
5. **Failed** (position 4) — gave up

## API Reference

- List boards: `GET /boards`
- Get columns: `GET /boards/:id/columns`
- List cards: `GET /boards/:id/cards`
- Update card: `PUT /boards/:id/cards/:card_id` (body: JSON fields to update)
- Get comments: `GET /cards/:card_id/comments`
- Add comment: `POST /cards/:card_id/comments` (body: `{"body": "..."}`)

## Important Rules

- Always add a comment before moving a card — comments are the audit trail
- Never delete cards, only move them to Done or Failed
- Research the codebase thoroughly before declaring something complete
- If unsure whether something is truly done, say partially done and ask