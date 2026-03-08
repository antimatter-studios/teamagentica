# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/infra-cron-scheduler

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/infra-cron-scheduler/ /app/plugins/infra-cron-scheduler/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/infra-cron-scheduler/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-cron-scheduler/ plugins/infra-cron-scheduler/
WORKDIR /app/plugins/infra-cron-scheduler
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-cron-scheduler-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /infra-cron-scheduler-plugin /usr/local/bin/infra-cron-scheduler-plugin
USER appuser
CMD ["infra-cron-scheduler-plugin"]
