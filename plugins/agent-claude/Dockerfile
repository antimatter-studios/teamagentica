# ── Claude CLI installer ──
FROM node:22-alpine AS claude-installer

RUN npm install -g @anthropic-ai/claude-code \
    && which claude

# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git nodejs npm \
    && go install github.com/air-verse/air@latest

COPY --from=claude-installer /usr/local/lib/node_modules/@anthropic-ai/claude-code /usr/local/lib/node_modules/@anthropic-ai/claude-code
COPY --from=claude-installer /usr/local/bin/claude /usr/local/bin/claude

WORKDIR /app

COPY . .

RUN go mod download

RUN mkdir -p /data/claude-workspace

EXPOSE 8082
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-claude .

# ── Prod stage ──
FROM node:22-alpine AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=claude-installer /usr/local/lib/node_modules/@anthropic-ai/claude-code /usr/local/lib/node_modules/@anthropic-ai/claude-code
COPY --from=claude-installer /usr/local/bin/claude /usr/local/bin/claude
COPY --from=builder /agent-claude /usr/local/bin/agent-claude
RUN mkdir -p /data/claude-workspace && chown -R appuser:appuser /data

USER appuser
EXPOSE 8082
CMD ["agent-claude"]
