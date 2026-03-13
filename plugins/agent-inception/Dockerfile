# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

RUN mkdir -p /data

EXPOSE 8085
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-inception .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /agent-inception /usr/local/bin/agent-inception
RUN mkdir -p /data && chown -R appuser:appuser /data

USER appuser
EXPOSE 8085
CMD ["agent-inception"]
