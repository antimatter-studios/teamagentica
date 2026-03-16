# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-volume/ plugins/storage-volume/
WORKDIR /build/plugins/storage-volume

RUN go mod download

EXPOSE 8090
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-volume/ plugins/storage-volume/
WORKDIR /build/plugins/storage-volume
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /storage-volume .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica
RUN mkdir -p /data && chown -R teamagentica:teamagentica /data
COPY --from=builder /storage-volume /usr/local/bin/storage-volume
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/storage-volume/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8090
CMD ["storage-volume"]
