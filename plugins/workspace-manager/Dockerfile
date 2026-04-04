# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git \
    && go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/workspace-manager/ plugins/workspace-manager/
WORKDIR /build/plugins/workspace-manager

RUN go mod download

EXPOSE 8091
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/workspace-manager/ plugins/workspace-manager/
WORKDIR /build/plugins/workspace-manager
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /workspace-manager .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates git \
    && adduser -D -u 10001 teamagentica
RUN mkdir -p /workspaces && chown -R teamagentica:teamagentica /workspaces
COPY --from=builder /workspace-manager /usr/local/bin/workspace-manager
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/workspace-manager/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8091
CMD ["workspace-manager"]
