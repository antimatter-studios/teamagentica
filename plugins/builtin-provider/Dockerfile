# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/builtin-provider

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/builtin-provider/ /app/plugins/builtin-provider/

RUN go mod download

EXPOSE 8083
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
# Build from repo root: docker build -f plugins/builtin-provider/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/builtin-provider/ plugins/builtin-provider/
WORKDIR /app/plugins/builtin-provider
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /builtin-provider .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /builtin-provider /usr/local/bin/builtin-provider
USER appuser
EXPOSE 8083
CMD ["builtin-provider"]
