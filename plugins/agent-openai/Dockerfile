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

WORKDIR /app

COPY . .

RUN go mod download

RUN mkdir -p /data/codex-workspace

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-openai .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=codex-installer /usr/local/bin/codex /usr/local/bin/codex
COPY --from=builder /agent-openai /usr/local/bin/agent-openai
RUN mkdir -p /data/codex-workspace && chown -R appuser:appuser /data

USER appuser
EXPOSE 8081
CMD ["agent-openai"]
