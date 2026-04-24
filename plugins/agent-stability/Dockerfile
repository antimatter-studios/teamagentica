# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-stability/ plugins/agent-stability/
WORKDIR /build/plugins/agent-stability

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-stability/ plugins/agent-stability/
WORKDIR /build/plugins/agent-stability
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /stability-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica
COPY --from=builder /stability-plugin /usr/local/bin/stability-plugin
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-stability/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
CMD ["stability-plugin"]
