# ── Claude CLI installer ──
FROM node:22-alpine AS claude-installer

RUN npm install -g @anthropic-ai/claude-code \
    && which claude

# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git nodejs npm \
    && go install github.com/air-verse/air@latest

COPY --from=claude-installer /usr/local/lib/node_modules/@anthropic-ai/claude-code /usr/local/lib/node_modules/@anthropic-ai/claude-code
COPY --from=claude-installer /usr/local/bin/claude /usr/local/bin/claude

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-claude/ plugins/agent-claude/
WORKDIR /build/plugins/agent-claude

RUN go mod download

RUN mkdir -p /data/claude-workspace

EXPOSE 8082
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-claude/ plugins/agent-claude/
WORKDIR /build/plugins/agent-claude
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-claude .

# ── Prod stage ──
FROM node:22-alpine AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=claude-installer /usr/local/lib/node_modules/@anthropic-ai/claude-code /usr/local/lib/node_modules/@anthropic-ai/claude-code
COPY --from=claude-installer /usr/local/bin/claude /usr/local/bin/claude
COPY --from=builder /agent-claude /usr/local/bin/agent-claude
RUN mkdir -p /data/claude-workspace && chown -R teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-claude/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8082
CMD ["agent-claude"]
