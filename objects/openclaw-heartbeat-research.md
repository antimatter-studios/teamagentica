# OpenClaw & Hermes Agent Research

## Status: Complete

## OpenClaw
- **What is it?** Open-source framework for autonomous AI agents. MIT-licensed, 163k GitHub stars, 5700+ community skills, 50+ messaging integrations. Self-hosted, model-agnostic (Claude, GPT-4, Ollama, etc.). Stores all data locally in Markdown files.
- **Architecture:** Hub-and-spoke with 5 core components:
  1. **Gateway** — WebSocket server routing messages from channels (Slack, WhatsApp, etc.)
  2. **Brain** — orchestrates LLM calls using ReAct reasoning loop
  3. **Memory** — persistent context stored in Markdown files
  4. **Skills** — plug-in capabilities for actions
  5. **Heartbeat** — schedules tasks and monitors inboxes

### OpenClaw Heartbeat Mechanism (detailed)
The term "heartbeat" in OpenClaw has TWO distinct meanings:

**1. Agent Heartbeat (proactive behavior):**
- Scheduled agent run at regular intervals (default: 30 min) in the main session
- Reads a checklist from `HEARTBEAT.md` in the workspace
- Agent checks inbox, calendar, notifications, project status, etc. in one batched run
- Smart filtering: if nothing needs attention, agent replies `HEARTBEAT_OK` and no message is delivered
- Runs share the same main session — agent remembers recent conversations
- Unlike cron jobs, heartbeat turns do NOT create task records
- Deterministic per-job stagger of up to 5 min for top-of-hour expressions (avoids load spikes)

**2. Gateway Health Monitoring (liveness):**
- Gateway exposes health/status methods through WebSocket protocol
- Periodic tick events serve as liveness signals (heartbeat for monitoring client connections)
- `openclaw health --json` returns health snapshot: linked creds, per-channel probe summaries, session-store summary, probe duration
- `gateway.channelHealthCheckMinutes` controls channel health check frequency
- A2A plugin uses exponential backoff + circuit breaker (closed -> open -> half-open) for peer health

### OpenClaw Cron vs Heartbeat
- **Cron:** Scheduled system event, creates task record, silent notify. For precise schedules (daily reports, weekly reviews)
- **Heartbeat:** Scheduled main-session turn, NO task record. For periodic "check on things" behavior
- **Key insight:** Agent heartbeat is NOT a health/liveness check — it's proactive agent behavior

## Hermes Agent
- **What is it?** Self-improving AI agent framework by Nous Research. Open-source, released Feb 2026. Built-in learning loop: creates skills from experience, improves them during use, persists knowledge, searches past conversations.
- **Architecture:** Modular Python-based:
  - `run_agent.py` — AIAgent core loop
  - `model_tools.py` — tool discovery/orchestration
  - `hermes_state.py` — SQLite session/state database
  - `gateway` — long-running orchestration layer for platform adapters, session routing, pairing, delivery, cron ticking
  - `cron` — scheduled jobs
  - `memory` — provider plugins
  - `tools` — implementations
- **Channels:** Telegram, Discord, Slack, WhatsApp, Signal, Email, CLI from single gateway
- **Terminal backends:** local, Docker, SSH, Daytona, Singularity, Modal

### Hermes Liveness / Health
- **No application-level heartbeat.** Hermes relies entirely on OS-level process supervision.
- **Gateway = the process:** Single long-running background process that connects to all platforms, handles sessions, runs cron, delivers voice messages. If gateway is running, agent is alive.
- **systemd service management:** `hermes gateway install/start/stop/status`. Uses systemd for automatic restart, boot persistence.
- **Multiple profiles:** Each profile gets its own service name (e.g., `hermes-gateway-coder`), managed independently.
- **Future plans (Issue #344):** Multi-agent architecture plans include periodic health checks for sub-agents, stuck detection (no tool calls for N seconds), and escalation.
- **Philosophy:** Hermes is a monolith — gateway, agent loop, tools all in one process. No need for inter-component health checks.

## Comparison with TeamAgentica

| Aspect | TeamAgentica | OpenClaw | Hermes |
|--------|-------------|----------|--------|
| **Architecture** | Microkernel + plugin containers | Monolith (Gateway + Brain + Memory + Skills) | Monolith (gateway + agent loop + tools) |
| **Health model** | Kernel sends heartbeats to plugin containers, marks unhealthy/stopped | Gateway WebSocket tick events + channel health probes + CLI health command | systemd process supervision only |
| **"Heartbeat" meaning** | Liveness check (is plugin alive?) | Dual: (1) proactive agent behavior (check inbox/calendar), (2) gateway tick liveness | N/A — no heartbeat concept |
| **Plugin/component isolation** | Each plugin = separate Docker container | Skills are in-process modules | Tools are in-process modules |
| **Why health checks exist** | Plugins are separate processes that can crash independently | Gateway needs to monitor channel connections (Slack/WhatsApp can disconnect) | Single process — if it dies, systemd restarts it |
| **Failure recovery** | Kernel detects unhealthy plugin, can restart container | Gateway reconnects channels automatically | systemd restarts the whole process |

### Key Takeaways

1. **OpenClaw's "heartbeat" is mostly about agent proactivity, not liveness.** The agent periodically wakes up to check if anything needs attention (inbox, calendar). The actual liveness monitoring is separate (gateway health endpoints + channel probes). This is fundamentally different from TeamAgentica's heartbeat which is purely a liveness signal.

2. **Hermes has no heartbeat because it's a monolith.** Everything runs in one process. Process supervision is delegated to systemd. No inter-component health checks needed because there are no separate components. This is the opposite of TeamAgentica's microkernel approach where each plugin is an independent container.

3. **TeamAgentica's approach is unique** among these three because it's the only one with a true multi-container architecture requiring inter-process liveness checks. OpenClaw and Hermes are both monoliths — skills/tools run in-process, so there's nothing to heartbeat.

4. **OpenClaw's channel health probes** are the closest analog to TeamAgentica's plugin heartbeats — both monitor external dependencies that can fail independently (channels vs containers).

## Sources
- [Milvus Blog - Complete Guide to OpenClaw](https://milvus.io/blog/openclaw-formerly-clawdbot-moltbot-explained-a-complete-guide-to-the-autonomous-ai-agent.md)
- [OpenClaw Docs - Agent Runtime](https://docs.openclaw.ai/concepts/agent)
- [OpenClaw GitHub](https://github.com/openclaw/openclaw)
- [OpenClaw Architecture - Substack](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
- [OpenClaw Docs - Cron vs Heartbeat](https://docs.openclaw.ai/automation/cron-vs-heartbeat)
- [OpenClaw Docs - Health Checks](https://docs.openclaw.ai/gateway/health)
- [OpenClaw Health Monitoring - DeepWiki](https://deepwiki.com/openclaw/openclaw/14.1-health-monitoring)
- [OpenClaw GitHub - cron-vs-heartbeat.md](https://github.com/openclaw/openclaw/blob/main/docs/automation/cron-vs-heartbeat.md)
- [Hermes Agent GitHub](https://github.com/nousresearch/hermes-agent)
- [Hermes Agent Architecture Docs](https://hermes-agent.nousresearch.com/docs/developer-guide/architecture/)
- [Hermes Agent Messaging Gateway Docs](https://hermes-agent.nousresearch.com/docs/user-guide/messaging/)
- [Hermes Agent Profiles Docs](https://hermes-agent.nousresearch.com/docs/user-guide/profiles/)
- [Hermes Agent Multi-Agent Issue #344](https://github.com/NousResearch/hermes-agent/issues/344)
- [MarkTechPost - Hermes Agent Release](https://www.marktechpost.com/2026/02/26/nous-research-releases-hermes-agent-to-fix-ai-forgetfulness-with-multi-level-memory-and-dedicated-remote-terminal-access-support/)
- [OpenClaw A2A Gateway Plugin](https://github.com/win4r/openclaw-a2a-gateway)
- [OpenClaw Ops Skills](https://github.com/cathrynlavery/openclaw-ops)
