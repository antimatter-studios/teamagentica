# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git \
    && go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-workspace-manager/ plugins/infra-workspace-manager/
WORKDIR /build/plugins/infra-workspace-manager

RUN go mod download

EXPOSE 8091
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-workspace-manager/ plugins/infra-workspace-manager/
WORKDIR /build/plugins/infra-workspace-manager
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-workspace-manager .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates git \
    && adduser -D -u 10001 teamagentica
RUN mkdir -p /workspaces && chown -R teamagentica:teamagentica /workspaces
COPY --from=builder /infra-workspace-manager /usr/local/bin/infra-workspace-manager
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-workspace-manager/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8091
CMD ["infra-workspace-manager"]
