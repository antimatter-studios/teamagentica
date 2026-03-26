# -- Build Go sidecar --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-memory/ plugins/infra-agent-memory/
WORKDIR /build/plugins/infra-agent-memory
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-agent-memory .

# -- Prod stage: Python + Mem0 + Go sidecar + supervisord --
FROM python:3.12-slim AS prod

RUN apt-get update && apt-get install -y --no-install-recommends \
        supervisor ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Mem0 with Qdrant support
RUN pip install --no-cache-dir mem0ai[qdrant] uvicorn fastapi ollama

# Create app user and directories
RUN useradd -r -u 10001 teamagentica \
    && mkdir -p /data /var/log/supervisor /usr/local/etc/teamagentica \
    && chown -R teamagentica:teamagentica /data

# Copy Go sidecar binary
COPY --from=builder /infra-agent-memory /usr/local/bin/infra-agent-memory

# Copy Mem0 server launcher
COPY plugins/infra-agent-memory/mem0_server.py /app/mem0_server.py

# Copy configs
COPY plugins/infra-agent-memory/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY plugins/infra-agent-memory/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

EXPOSE 8091 8010
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
