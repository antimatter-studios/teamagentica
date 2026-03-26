# -- Build Go sidecar --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-ollama/ plugins/agent-ollama/
WORKDIR /build/plugins/agent-ollama
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-ollama .

# -- Prod stage: Ollama + Go sidecar + supervisord --
FROM ollama/ollama:latest AS prod

RUN apt-get update && apt-get install -y --no-install-recommends \
        supervisor ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /data /var/log/supervisor /usr/local/etc/teamagentica

# Copy Go sidecar binary
COPY --from=builder /agent-ollama /usr/local/bin/agent-ollama

# Copy configs
COPY plugins/agent-ollama/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY plugins/agent-ollama/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

EXPOSE 11434 8083
ENTRYPOINT []
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
