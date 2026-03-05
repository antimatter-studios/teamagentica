# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache gcc musl-dev
RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/chat

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/chat/ /app/plugins/chat/

RUN go mod download

EXPOSE 8092
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/chat/ plugins/chat/
WORKDIR /app/plugins/chat
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /chat .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /chat /usr/local/bin/chat
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser
EXPOSE 8092
CMD ["chat"]
