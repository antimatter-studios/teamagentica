#!/bin/bash
set -e

# Common setup (Claude config symlink, backup restore)
source /usr/local/bin/entrypoint-common.sh

# --- Agent Bridge setup ---

export AGENT_BRIDGE_AGENT="${AGENT_BRIDGE_AGENT:-claude}"
export AGENT_BRIDGE_SESSION="${AGENT_BRIDGE_SESSION:-$(hostname)}"
export AGENT_BRIDGE_PORT="${AGENT_BRIDGE_PORT:-9999}"

echo "agent-bridge: agent=${AGENT_BRIDGE_AGENT} port=${AGENT_BRIDGE_PORT} session=${AGENT_BRIDGE_SESSION}"

# supervisord manages portpilot (auto-restart) and agent-bridge (auto-restart).
# portpilot on :7681 proxies to agent-bridge on :9999 by default,
# and auto-detects any dev server ports the agent spins up.
exec supervisord -c /etc/supervisord.conf
