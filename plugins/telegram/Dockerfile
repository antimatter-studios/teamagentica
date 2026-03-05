# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/telegram

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/telegram/ /app/plugins/telegram/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/telegram/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/telegram/ plugins/telegram/
WORKDIR /app/plugins/telegram
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /telegram-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /telegram-plugin /usr/local/bin/telegram-plugin
USER appuser
CMD ["telegram-plugin"]
