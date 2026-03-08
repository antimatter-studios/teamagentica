# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/messaging-telegram

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/messaging-telegram/ /app/plugins/messaging-telegram/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/messaging-telegram/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/messaging-telegram/ plugins/messaging-telegram/
WORKDIR /app/plugins/messaging-telegram
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /messaging-telegram .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /messaging-telegram /usr/local/bin/messaging-telegram
USER appuser
CMD ["messaging-telegram"]
