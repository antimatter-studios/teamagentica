# TeamAgentica code-server workspace image
# VS Code (code-server) + dev tools (go, bun, node) + portpilot for path routing.
# portpilot on :8080 strips PROXY_BASE_PATH, forwards to code-server on :8081.
#
# Build: docker build -t teamagentica-code-server:latest images/code-server/
# Run:   docker run -p 8080:8080 teamagentica-code-server:latest

FROM codercom/code-server:latest

USER root

# Dev tools: git, neovim, jq, tmux (code-server base already has node/npm/curl)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    neovim \
    jq \
    tmux \
    unzip \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

# Go toolchain
RUN curl -fsSL https://go.dev/dl/go1.24.3.linux-$(dpkg --print-architecture).tar.gz | tar xz -C /usr/local
ENV PATH="/usr/local/go/bin:${PATH}"

# Bun (to /usr/local/bin so all users can access it)
RUN curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local bash

# Portpilot (port detection + reverse proxy)
RUN GONOSUMCHECK=github.com/antimatter-studios/portpilot GOFLAGS=-mod=mod GONOSUMDB=github.com/antimatter-studios/portpilot go install github.com/antimatter-studios/portpilot@v0.2.0 && \
    cp /root/go/bin/portpilot /usr/local/bin/portpilot && \
    rm -rf /root/go

# Ensure coder user has Go + Bun in PATH
RUN echo 'export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"' >> /home/coder/.bashrc

# Shell safety: treat unbound variables as errors (prevents rm -rf /${UNSET_VAR} disasters)
RUN echo 'set -o nounset' > /etc/profile.d/shell-safety.sh

COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh
COPY supervisord.conf /etc/supervisord.conf

USER 1000

ENTRYPOINT ["entrypoint.sh"]
