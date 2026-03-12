# DevBox Agent — agent-driven workspace with no terminal.
# Agent-bridge is the sole interface; relay connects over TCP.
# Layers on top of the devbox base image.
#
# Build: docker build -t teamagentica-devbox-agent:latest images/devbox-agent/
# Run:   docker run -p 7681:7681 -e AGENT_BRIDGE_AGENT=claude teamagentica-devbox-agent:latest

FROM teamagentica-devbox:latest

USER root

COPY supervisord.conf /etc/supervisord.conf
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh

USER coder
WORKDIR /workspace

EXPOSE 7681

# supervisord manages:
#   portpilot on :7681 (external) → auto-detects dev server ports
#   agent-bridge on :9999 (internal) → CLI agent with session continuity
# portpilot auto-forwards agent-bridge port externally.
ENTRYPOINT ["entrypoint.sh"]
