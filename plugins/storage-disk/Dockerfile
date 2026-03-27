# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-disk/ plugins/storage-disk/
WORKDIR /build/plugins/storage-disk

RUN go mod download

EXPOSE 8090
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-disk/ plugins/storage-disk/
WORKDIR /build/plugins/storage-disk
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /storage-disk .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica
RUN mkdir -p /data && chown -R teamagentica:teamagentica /data
COPY --from=builder /storage-disk /usr/local/bin/storage-disk
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/storage-disk/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8090
CMD ["storage-disk"]
