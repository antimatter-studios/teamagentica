# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/tool-veo

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/tool-veo/ /app/plugins/tool-veo/

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/tool-veo/ plugins/tool-veo/
WORKDIR /app/plugins/tool-veo
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /tool-veo .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /tool-veo /usr/local/bin/tool-veo
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser
EXPOSE 8081
CMD ["tool-veo"]
