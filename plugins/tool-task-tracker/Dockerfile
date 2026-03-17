# -- Dev stage --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git gcc musl-dev
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/tool-task-tracker/ plugins/tool-task-tracker/
WORKDIR /build/plugins/tool-task-tracker

RUN go mod download

EXPOSE 8093
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/tool-task-tracker/ plugins/tool-task-tracker/
WORKDIR /build/plugins/tool-task-tracker
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /tool-task-tracker .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /tool-task-tracker /usr/local/bin/tool-task-tracker
RUN mkdir -p /data && chown teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/tool-task-tracker/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8093
CMD ["tool-task-tracker"]
