# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/whatsapp

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/whatsapp/ /app/plugins/whatsapp/

RUN go mod download

EXPOSE 8091
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/whatsapp/ plugins/whatsapp/
WORKDIR /app/plugins/whatsapp
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /whatsapp-bot .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /whatsapp-bot /usr/local/bin/whatsapp-bot
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser
EXPOSE 8091
CMD ["whatsapp-bot"]
