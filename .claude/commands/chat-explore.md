Explore Team Agentica architecture by chatting with an agent (specified by @alias) through the web chat API.

Arguments: $ARGUMENTS

The first word of the arguments MUST be an @alias (e.g. `@chat`, `@architect`). This is the agent to converse with. Everything after the alias is the topic.

Examples:
- "@chat observability and debugging"
- "@architect security testing gaps"
- "@chat how the relay handles streaming"

## Setup

Resolve the chat API and authentication:
```
BASE="http://localhost:9741/api/route/messaging-chat"
TOKEN from ~/.config/teamagentica/tacli.json profiles[0].token
TRACKER="http://api.teamagentica.localhost/api/route/tool-task-tracker"
```

## Workflow

### 1. Create a new conversation
```
POST $BASE/conversations
Body: {"title": "<topic summary>"}
```
Save the conversation ID.

### 2. Send messages prefixed with the alias
Every message to the agent MUST start with the @alias parsed from the arguments.

Send informed, specific questions based on the topic. Include:
- What the platform currently does (from git log, source code, or memory)
- Specific architectural questions
- Requests for design proposals
- Provocative questions that challenge assumptions

### 3. Wait for replies
The response is async. Wait 50-70 seconds, then poll:
```
GET $BASE/conversations/:id
```
Extract messages with id > last_seen_id. The agent's replies may be in `role: "assistant"` messages.

### 4. Handle context limits
If the agent returns `argument list too long` or `exit status 1`, the conversation context is too long. Create a new conversation and continue the topic there. Reference what was discussed so the agent can recover from memory.

### 5. Handle cut-off responses
If a response is clearly truncated mid-sentence, send a follow-up asking the agent to finish that specific thought. Do NOT drop the topic - insist on completing it.

### 6. Track actionable items
When the agent proposes something genuinely useful or novel, add it as a task card to the Architecture Chat Backlog board:
```
POST $TRACKER/boards/<board_id>/cards
Body: {"title": "...", "description": "...", "priority": "...", "labels": "...", "column_id": "<todo_column_id>"}
```

Use these priority guidelines:
- **critical**: Active bugs, security vulnerabilities
- **high**: Blocks other work, high impact, low effort
- **medium**: Important but not blocking, moderate effort
- **low**: Nice to have, future vision, high effort

### 7. Incorporate user feedback
The human user may provide corrections, additional context, or new topics via IDE messages while the conversation is ongoing. Weave their feedback into the next message to the agent.

## Conversation Style

- Be specific and technical, not vague
- Share actual source code findings, git commit messages, and architecture details
- Ask the agent to design solutions, not just identify problems
- Challenge the agent's assumptions when appropriate
- Ask follow-up questions on interesting ideas
- When the agent proposes something cool, ask "how would you implement this?"

## Board Reference

The Architecture Chat Backlog board ID and column IDs can be found via:
```
GET $TRACKER/boards
```
Look for the board named "Architecture Chat Backlog". The Todo column is position 0.

## Output

After the exploration session, summarize:
- Topics covered
- Number of new task cards created
- Key insights and design decisions
- Open questions for next session
