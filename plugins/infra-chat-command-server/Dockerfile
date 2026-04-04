# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-chat-command-server/ plugins/infra-chat-command-server/
WORKDIR /build/plugins/infra-chat-command-server

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-chat-command-server/ plugins/infra-chat-command-server/
WORKDIR /build/plugins/infra-chat-command-server
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /infra-chat-command-server .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /infra-chat-command-server /usr/local/bin/infra-chat-command-server

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-chat-command-server/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["infra-chat-command-server"]
