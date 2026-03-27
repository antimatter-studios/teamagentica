# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-task-scheduler/ plugins/infra-task-scheduler/
WORKDIR /build/plugins/infra-task-scheduler

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-task-scheduler/ plugins/infra-task-scheduler/
WORKDIR /build/plugins/infra-task-scheduler
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /infra-task-scheduler-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica
COPY --from=builder /infra-task-scheduler-plugin /usr/local/bin/infra-task-scheduler-plugin
RUN mkdir -p /data && chown teamagentica:teamagentica /data
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-task-scheduler/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["infra-task-scheduler-plugin"]
