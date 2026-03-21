Fix a bug using a structured fix-test-iterate loop. Uses the task-flow skill for board operations.

Arguments: $ARGUMENTS

The arguments can be:
- A board name/ID + card title to work on an existing card
- A description of a bug to create a card for and fix
- A card URL or ID

## Setup

Use the **task-flow** skill conventions for all board operations (setup, card resolution, column moves, comments, retry flow, reporting).

## Workflow

### 1. Resolve or create the card
Use task-flow to find or create the card. Move it to In Progress with comment: `"Started fix (attempt 1/10)"`

### 2. Review comments before fixing
Use task-flow to read all comments. Look for context that affects the fix approach. If comments raise valid concerns, add a comment acknowledging the adjusted approach.

### 3. Dispatch a background agent
Launch a background agent (subagent_type: general-purpose) with:
- The card title and full description
- Any relevant comments/questions from other users
- Instructions to:
  1. Read the relevant source code at the locations identified
  2. Consider any user feedback from card comments
  3. Implement the minimal fix
  4. If tests exist for this area, run them
  5. If no tests exist and the fix is testable, write one
  6. Run `go build ./...` for any Go changes
  7. Report: what was changed, what tests were run, pass/fail

### 4. Evaluate the result
When the agent completes:

**If tests PASS:**
- Add a detailed comment via task-flow explaining:
  - What the bug was
  - What was changed to fix it
  - What tests were run and their results
  - Any residual risk or caveats
- Move card to Done via task-flow

**If tests FAIL:**
- Use task-flow retry flow: add attempt comment, increment attempt
- If under max attempts: dispatch another agent with failure context, go back to Step 3
- If at max attempts: task-flow moves card to Failed

### 5. Report
Use task-flow summary table format.

## Important Rules

- Never skip the test step — every fix must be verified
- Run `go build ./...` before testing Go changes
- For kernel changes: rebuild with `task build` (from kernel/ dir) and restart with `task restart`
- For plugin changes: use the plugin-deploy skill
- Keep fixes minimal — don't refactor surrounding code
- One fix per agent — don't combine unrelated changes
- If a fix requires architectural changes beyond a single file, document it and flag for manual review instead of attempting the fix
