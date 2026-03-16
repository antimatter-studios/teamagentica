# Grab sss3 binary from upstream image
FROM ghcr.io/espebra/stupid-simple-s3:latest AS sss3

# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-sss3/ plugins/storage-sss3/
WORKDIR /build/plugins/storage-sss3

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/storage-sss3/ plugins/storage-sss3/
WORKDIR /build/plugins/storage-sss3
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /storage-sss3 .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica
RUN mkdir -p /data/sss3 /data/sss3-tmp && chown -R teamagentica:teamagentica /data
COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3
COPY --from=builder /storage-sss3 /usr/local/bin/storage-sss3
RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/storage-sss3/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
CMD ["storage-sss3"]
