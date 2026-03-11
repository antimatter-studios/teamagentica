# AI DevBox workspace image
# Web terminal (ttyd) with AI coding CLIs: Claude Code, OpenAI Codex, OpenCode.
# Set DEVBOX_APP env var to control which CLI launches (default: bash).
#
# Build: docker build -t teamagentica-devbox:latest images/devbox/
# Run:   docker run -p 7681:7681 -e DEVBOX_APP=claude teamagentica-devbox:latest

FROM node:22-bookworm-slim

# Detect architecture for ttyd binary download
ARG TARGETARCH

# System deps: git, common dev tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    ca-certificates \
    openssh-client \
    less \
    neovim \
    jq \
    unzip \
    tmux \
    supervisor \
    libwebsockets-dev \
    libjson-c-dev \
    && rm -rf /var/lib/apt/lists/*

# Install ttyd from GitHub releases (not in Debian bookworm repos)
RUN TTYD_ARCH=$(case "${TARGETARCH}" in "amd64") echo "x86_64" ;; "arm64") echo "aarch64" ;; *) echo "x86_64" ;; esac) && \
    curl -fsSL "https://github.com/tsl0922/ttyd/releases/latest/download/ttyd.${TTYD_ARCH}" -o /usr/local/bin/ttyd && \
    chmod +x /usr/local/bin/ttyd

# Install AI coding CLIs globally
RUN npm install -g @anthropic-ai/claude-code @openai/codex opencode-ai && npm cache clean --force

# Install Go toolchain
RUN curl -fsSL https://go.dev/dl/go1.24.3.linux-$(dpkg --print-architecture).tar.gz | tar xz -C /usr/local
ENV PATH="/usr/local/go/bin:${PATH}"

# Install Bun (to /usr/local/bin so all users can access it)
RUN curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local bash

# Install portpilot (port detection + reverse proxy)
RUN GONOSUMCHECK=github.com/antimatter-studios/portpilot GOFLAGS=-mod=mod GONOSUMDB=github.com/antimatter-studios/portpilot go install github.com/antimatter-studios/portpilot@v0.2.0 && \
    cp /root/go/bin/portpilot /usr/local/bin/portpilot

# Create non-root user (GID 1000 exists as 'node' in base image)
RUN useradd -m -u 1001 -s /bin/bash coder && \
    echo 'export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"' >> /home/coder/.bashrc

# Workspace directory
RUN mkdir -p /workspace && chown coder:coder /workspace

# Claude config directory (shared volume mount point)
RUN mkdir -p /home/coder/.claude && chown coder:coder /home/coder/.claude

# Supervisord config and scripts
COPY supervisord.conf /etc/supervisord.conf
COPY --chmod=755 ttyd-exit-handler.sh /usr/local/bin/ttyd-exit-handler.sh
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh

USER coder
WORKDIR /workspace

EXPOSE 7681

# supervisord (PID 1) manages:
#   portpilot on :7681 (external) → proxies / to ttyd on :7682
#   ttyd on :7682 (internal) → tmux → $DEVBOX_APP
# If ttyd dies, container exits. portpilot auto-restarts.
# Auto-detected dev server ports available at /proxy/{port}/
ENTRYPOINT ["entrypoint.sh"]
