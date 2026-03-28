# -- Build Go sidecar --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-memory-lcm/ plugins/infra-agent-memory-lcm/
WORKDIR /build/plugins/infra-agent-memory-lcm
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w -linkmode external -extldflags '-static'" -o /infra-agent-memory-lcm .

# -- Install Node.js dependencies --
FROM node:22-slim AS node-builder
WORKDIR /app
COPY plugins/infra-agent-memory-lcm/lcm-server/package.json plugins/infra-agent-memory-lcm/lcm-server/package-lock.json* ./
RUN npm install --legacy-peer-deps
COPY plugins/infra-agent-memory-lcm/lcm-server/*.ts ./

# -- Prod stage: Go sidecar + Node.js LCM server + supervisord --
FROM node:22-slim AS prod

RUN apt-get update && apt-get install -y --no-install-recommends \
        supervisor ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install tsx globally for running TypeScript
RUN npm install -g tsx

# Create app user and directories
RUN useradd -r -u 10001 teamagentica \
    && mkdir -p /data /var/log/supervisor /usr/local/etc/teamagentica \
    && chown -R teamagentica:teamagentica /data

# Copy Go sidecar binary
COPY --from=builder /infra-agent-memory-lcm /usr/local/bin/infra-agent-memory-lcm

# Copy Node.js LCM server with dependencies
COPY --from=node-builder /app /app/lcm-server

# Copy configs
COPY plugins/infra-agent-memory-lcm/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY plugins/infra-agent-memory-lcm/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

EXPOSE 8091 8092
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
