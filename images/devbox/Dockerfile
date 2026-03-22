# DevBox base image — tools and runtimes, no user interface.
# Use devbox-terminal or devbox-agent as the actual workspace image.
#
# Build: docker build -t teamagentica-devbox:latest -f images/devbox/Dockerfile .

# --- Kaniko executor (OCI image builder, no Docker daemon needed) ---
FROM gcr.io/kaniko-project/executor:latest AS kaniko

# --- tacli builder (TeamAgentica CLI) ---
FROM golang:1.24-bookworm AS tacli-builder
WORKDIR /build
COPY tacli/ tacli/
RUN cd tacli && CGO_ENABLED=0 go build -ldflags="-s -w" -o /tacli .

FROM node:22-bookworm-slim

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
    procps \
    tmux \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

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

# Install agent-bridge (TCP-to-CLI bridge for agent-driven workspaces)
RUN GONOSUMCHECK=github.com/antimatter-studios/agentbridge GOFLAGS=-mod=mod GONOSUMDB=github.com/antimatter-studios/agentbridge go install github.com/antimatter-studios/agentbridge@latest && \
    cp /root/go/bin/agentbridge /usr/local/bin/agent-bridge

# Create non-root user (GID 1000 exists as 'node' in base image)
RUN useradd -m -u 1001 -s /bin/bash coder && \
    echo 'export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"' >> /home/coder/.bashrc

# Workspace directory
RUN mkdir -p /workspace && chown coder:coder /workspace

# Claude config directory (shared volume mount point)
RUN mkdir -p /home/coder/.claude && chown coder:coder /home/coder/.claude

# Kaniko executor (builds OCI images without Docker daemon)
COPY --from=kaniko /kaniko/executor /usr/local/bin/kaniko

# tacli (TeamAgentica CLI for plugin management)
COPY --from=tacli-builder /tacli /usr/local/bin/tacli

# Common entrypoint setup (Claude config symlink, backup restore)
COPY --chmod=755 images/devbox/entrypoint-common.sh /usr/local/bin/entrypoint-common.sh

USER coder
WORKDIR /workspace

EXPOSE 7681
