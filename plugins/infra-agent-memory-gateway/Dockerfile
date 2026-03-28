# -- Build Go binary --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-memory-gateway/ plugins/infra-agent-memory-gateway/
WORKDIR /build/plugins/infra-agent-memory-gateway
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w -linkmode external -extldflags '-static'" -o /infra-agent-memory-gateway .

# -- Prod stage: pure Go, no Python/supervisord needed --
FROM debian:bookworm-slim AS prod

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -r -u 10001 teamagentica \
    && mkdir -p /data /usr/local/etc/teamagentica \
    && chown -R teamagentica:teamagentica /data

COPY --from=builder /infra-agent-memory-gateway /usr/local/bin/infra-agent-memory-gateway
COPY plugins/infra-agent-memory-gateway/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml

EXPOSE 8091
CMD ["/usr/local/bin/infra-agent-memory-gateway"]
