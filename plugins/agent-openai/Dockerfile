# ── Codex CLI installer ──
FROM alpine:3.19 AS codex-installer

ARG TARGETARCH
ARG CODEX_VERSION=0.107.0

RUN apk add --no-cache curl tar \
    && if [ "$TARGETARCH" = "arm64" ]; then \
         TRIPLE="aarch64-unknown-linux-musl"; \
       else \
         TRIPLE="x86_64-unknown-linux-musl"; \
       fi \
    && curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-${TRIPLE}.tar.gz" \
       -o /tmp/codex.tar.gz \
    && tar -xzf /tmp/codex.tar.gz -C /tmp/ \
    && mv /tmp/codex-${TRIPLE} /usr/local/bin/codex \
    && chmod +x /usr/local/bin/codex \
    && rm /tmp/codex.tar.gz

# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

COPY --from=codex-installer /usr/local/bin/codex /usr/local/bin/codex

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-openai/ plugins/agent-openai/
WORKDIR /build/plugins/agent-openai

RUN go mod download

RUN mkdir -p /data/codex-workspace

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-openai/ plugins/agent-openai/
WORKDIR /build/plugins/agent-openai
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /agent-openai .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /codex-exec-server ./cmd/exec-server

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=codex-installer /usr/local/bin/codex /usr/local/bin/codex
COPY --from=builder /agent-openai /usr/local/bin/agent-openai
COPY --from=builder /codex-exec-server /usr/local/bin/codex-exec-server
RUN mkdir -p /data/codex-workspace && chown -R teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-openai/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["agent-openai"]
