# Agent Response Performance Optimization Results

Date: 2026-04-05

## Summary

Optimized the Codex (agent-openai) response pipeline from ~18s per message to ~3-5s for subsequent messages in the same conversation.

## Before (codex exec per-request)

Every message spawned a new `codex exec` process:
- Process startup: ~200ms (Rust, fast)
- API call: ~15s (no context caching)
- **Total: ~15-18s per message, every time**

## After (persistent app-server + thread reuse)

Single `codex app-server` process, threads reused per conversation:

| Stage | First Message | Subsequent Messages |
|---|---|---|
| Thread create | 5.5s | 0ms (cached) |
| Memory (store+history+facts) | ~800ms | ~500ms |
| Turn start | ~5ms | ~5ms |
| API first token | ~10s | ~2s (context cached) |
| Streaming | ~300ms | ~300ms |
| **Total** | **~18s** | **~3-5s** |

## Optimizations Applied

### 1. Persistent App-Server (websocket)
- `codex app-server` started once at plugin boot
- WebSocket connection (no compression — Codex's RSV bits break Go libraries)
- JSON-RPC protocol: initialize → thread/start → turn/start
- Eliminates per-request process spawn

### 2. Thread Reuse
- Map `sessionID` → `threadID` (conversation channel maps to Codex thread)
- Same conversation reuses the same thread — saves 5.5s per message
- Codex maintains conversation context internally, improving API response time

### 3. Parallel Memory Operations
- `memoryStore`, `memoryGetHistory`, `memorySearchFacts` run concurrently
- Reduced memory overhead from ~1.5s (sequential) to ~500ms (parallel)

### 4. Pipeline Timing Instrumentation
- Relay logs: `[timing] tg=<id> emit_thinking=Xms memory=Xms routing=Xms agent_call=Xms total=Xms`
- Agent logs: `[timing] thread=Xms turn_start=Xms first_token=Xms turn_done=Xms`

## Remaining Bottlenecks

1. **Mem0 embedding search** (~400-600ms): Requires embedding generation + vector search. Could be cached for repeated similar queries.
2. **Codex API thinking time** (~2-4s): Server-side, cannot be optimized locally.
3. **First message thread creation** (~5.5s): Happens once per conversation. Could pre-warm threads.

## Test Data

### Batch 4 (final consistency test, 10 messages same conversation):
```
msg 1:  5.4s      Min: 4.2s
msg 2:  5.4s      Max: 6.5s
msg 3:  4.3s      Avg: 5.0s (includes 1s polling overhead)
msg 4:  5.4s      Median: 5.4s
msg 5:  6.5s
msg 6:  4.3s      Relay internal avg: 4.1s
msg 7:  4.4s        memory: ~550ms
msg 8:  5.4s        agent_call: ~4.2s (Codex API)
msg 9:  4.3s
msg 10: 4.2s
```

### Improvement Summary
| Metric | Before | After | Improvement |
|---|---|---|---|
| First message | ~18s | ~18s | Same (thread create + cold API) |
| Subsequent messages | ~18s | ~4-5s | **3.6x faster** |
| Memory overhead | ~1.5s | ~0.5s | **3x faster** |
| Thread create (per conv) | 5.5s every msg | 5.5s once | **Eliminated for msg 2+** |
| CLI cold start | Per request | Once at boot | **Eliminated** |
