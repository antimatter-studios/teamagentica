# DevBox Terminal — human-facing workspace with web terminal (ttyd).
# Layers on top of the devbox base image.
#
# Build: docker build -t teamagentica-devbox-terminal:latest images/devbox-terminal/
# Run:   docker run -p 7681:7681 -e DEVBOX_APP=claude teamagentica-devbox-terminal:latest

FROM teamagentica-devbox:latest

USER root

ARG TARGETARCH

# ttyd deps + binary (not in Debian bookworm repos)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libwebsockets-dev \
    libjson-c-dev \
    && rm -rf /var/lib/apt/lists/* \
    && TTYD_ARCH=$(case "${TARGETARCH}" in "amd64") echo "x86_64" ;; "arm64") echo "aarch64" ;; *) echo "x86_64" ;; esac) \
    && curl -fsSL "https://github.com/tsl0922/ttyd/releases/latest/download/ttyd.${TTYD_ARCH}" -o /usr/local/bin/ttyd \
    && chmod +x /usr/local/bin/ttyd

COPY supervisord.conf /etc/supervisord.conf
COPY --chmod=755 ttyd-exit-handler.sh /usr/local/bin/ttyd-exit-handler.sh
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh

USER coder
WORKDIR /workspace

EXPOSE 7681

# supervisord manages:
#   portpilot on :7681 (external) → proxies / to ttyd on :7682
#   ttyd on :7682 (internal) → tmux → $DEVBOX_APP
# If ttyd dies, container exits. portpilot auto-restarts.
ENTRYPOINT ["entrypoint.sh"]
