# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git gcc musl-dev \
    && go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-relay/ plugins/infra-agent-relay/
WORKDIR /build/plugins/infra-agent-relay

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-relay/ plugins/infra-agent-relay/
WORKDIR /build/plugins/infra-agent-relay
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-agent-relay .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 10001 teamagentica
COPY --from=builder /infra-agent-relay /usr/local/bin/infra-agent-relay
RUN mkdir -p /usr/local/etc/teamagentica /data
COPY plugins/infra-agent-relay/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["infra-agent-relay"]
