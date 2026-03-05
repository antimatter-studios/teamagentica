# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/scheduler

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/scheduler/ /app/plugins/scheduler/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/scheduler/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/scheduler/ plugins/scheduler/
WORKDIR /app/plugins/scheduler
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /scheduler-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /scheduler-plugin /usr/local/bin/scheduler-plugin
USER appuser
CMD ["scheduler-plugin"]
