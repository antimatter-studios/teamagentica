# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git \
    && go install github.com/air-verse/air@latest

WORKDIR /app/plugins/infra-workspace-manager

COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/infra-workspace-manager/ /app/plugins/infra-workspace-manager/

RUN go mod download

EXPOSE 8091
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-workspace-manager/ plugins/infra-workspace-manager/
WORKDIR /app/plugins/infra-workspace-manager
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-workspace-manager .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache git \
    && adduser -D -u 1000 appuser
RUN mkdir -p /workspaces && chown -R appuser:appuser /workspaces
COPY --from=builder /infra-workspace-manager /usr/local/bin/infra-workspace-manager
USER appuser
EXPOSE 8091
CMD ["infra-workspace-manager"]
