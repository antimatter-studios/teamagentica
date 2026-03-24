# DAG Progress Streaming

## Problem

The relay's DAG executor is fully synchronous — it blocks on each `/chat` call until completion. This causes:
- Async tools like seedance (video generation, 2-5 min) hit the relay's task timeout (120s)
- No user feedback while tasks execute — the user stares at a blank response
- Long-running tasks tie up goroutines that could be doing other work

## Design

### Core Concept

The relay returns a `task_group_id` immediately when receiving a message. All subsequent progress and results are delivered via addressed events through the kernel event bus. Messaging clients (chat UI, Telegram, Discord) maintain a status bubble per `task_group_id` and update it as events arrive.

### Task Groups

A **task group** is a single user request processed by the coordinator. It contains:
- The coordinator call (planning phase)
- The DAG (list of tasks to execute)
- The synthesis step (final response)

Each task group gets a unique `task_group_id` that flows through the entire chain.

### `/chat` Contract

```
POST /chat
Request:  {"source_plugin": "...", "channel_id": "...", "message": "..."}
Response: {"task_group_id": "tg-abc123"} (HTTP 202)
```

The relay returns immediately. The messaging client creates a status bubble associated with `task_group_id` and starts a timer to show "Thinking..." with elapsed time.

### Event Flow

```
relay:progress {task_group_id, status: "thinking"}
relay:progress {task_group_id, status: "planning", message: "Planning tasks..."}
relay:progress {task_group_id, status: "running", message: "Asking @seedance..."}
relay:progress {task_group_id, status: "completed", response: "...", attachments: [...]}
relay:progress {task_group_id, status: "failed", message: "..."}
```

### SDK Helpers

- `RegisterWebhook(prefix)` — register route with webhook ingress
- `OnWebhookURL(fn)` — receive public URL when ngrok tunnel is ready
- `ReportRelayProgress(update)` — send progress event to relay

### Plugin Async Contract

Two modes for `/chat`:
- **Synchronous** (default): Plugin returns final response directly. Relay wraps in task group events.
- **Async** (opt-in): Plugin returns `{"status": "processing", "task_id": "..."}`. Relay waits for completion via event channel, resolved when webhook callback fires.

## Phases

### Phase 1: Progress event infrastructure [DONE]

- [x] SDK webhook helpers (`RegisterWebhook`, `OnWebhookURL`)
- [x] SDK progress helper (`ReportRelayProgress`)
- [x] Relay `relay:task:progress` event handler — forwards to source messaging plugin
- [x] Relay tracks last active session for progress routing
- [x] Messaging-chat `relay:progress` event handler — upserts `role: "progress"` messages
- [x] Messaging-chat cleans up progress messages on final response
- [x] Chat UI renders progress messages as muted italic markers
- [x] Chat UI polls every 3s while sending to pick up progress updates
- [x] Refactored Telegram webhook to use SDK helpers
- [x] Seedance webhook callback handler + callback_url in generate requests
- [x] Seedance `/chat` and `/models` endpoints
- [x] Relay type guard fix for TargetImage/TargetVideo in alias resolution

### Phase 2: Task group model + immediate return [DONE]

- [x] Generate `task_group_id` in relay's `handleChat`
- [x] Return `task_group_id` immediately to caller (HTTP 202)
- [x] Run orchestration in background goroutine (`processChat`)
- [x] Emit `relay:progress` events at state transitions (thinking, running, planning, completed/failed)
- [x] Emit final `completed` event with response text + attachments
- [x] Update messaging-chat to handle immediate return + event-based completion
- [x] Update chat UI — polls for progress + assistant message arrival
- [x] Update relay client in messaging-chat to accept `task_group_id` response
- [x] Chat store tracks `activeTaskGroupId`

### Phase 3: Async plugin support [DONE]

- [x] Define async `/chat` response format: `{"status": "processing", "task_id": "..."}`
- [x] Relay `callAgent` detects async responses and waits on event channel
- [x] Relay `relay:task:progress` handler resolves async waiters on completion
- [x] Seedance returns async when webhook callback is available, falls back to polling
- [x] Async waiter registration/cleanup in relay (`registerAsyncWaiter`/`removeAsyncWaiter`)

### Phase 4: Messaging platform integration [PARTIAL]

- [x] Telegram: sends "Thinking..." message, edits with progress, replaces with final response
- [x] Telegram: typing loop continues during task execution
- [x] Telegram relay client updated for task_group_id response
- [ ] Discord: `sendTyping()` loop + message edits (no Discord plugin yet)
- [ ] Define standard status vocabulary as constants in SDK

### Phase 5: Enhancements [TODO]

- [ ] Per-task DAG progress events (emit "task t1 started", "task t1 completed" during orchestration)
- [ ] DAG executor continues independent tasks while async ones are in-flight (currently goroutine blocks on async channel, but parallel tasks still run via separate goroutines)

### Future Considerations

- **Cancellation**: UX for cancelling in-flight task groups (button, command, new message). Need to see the system running before designing.
- **Multi-task-group**: User sends a new message while previous is still running. Queue? Cancel previous? Run in parallel?
- **Retry**: Allow re-running a failed task group or individual task.
- **Correlation IDs**: Currently uses last-active-session assumption. Board task created for multi-user correlation scheme.

## Notes

- `task_group_id` solves the correlation problem for multi-user — each request gets a unique ID
- No more 120s timeout — relay returns instantly, events flow asynchronously
- All inter-plugin communication uses the kernel event bus — no custom infrastructure
- The event-based model is more future-proof than synchronous request/response
- Polling fallback exists for seedance when ngrok/webhook-ingress is not running
