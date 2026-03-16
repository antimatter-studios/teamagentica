# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-builder/ plugins/infra-builder/
WORKDIR /build/plugins/infra-builder

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
COPY plugins/infra-builder/ plugins/infra-builder/
WORKDIR /build/plugins/infra-builder
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-builder .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /infra-builder /usr/local/bin/infra-builder

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-builder/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8090
CMD ["infra-builder"]
