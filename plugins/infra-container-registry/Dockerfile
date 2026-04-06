# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-container-registry/ plugins/infra-container-registry/
WORKDIR /build/plugins/infra-container-registry

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-container-registry/ plugins/infra-container-registry/
WORKDIR /build/plugins/infra-container-registry
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /registry-plugin .

# -- Prod stage: registry:2 + Go sidecar + supervisord --
FROM registry:2 AS prod
RUN apk add --no-cache supervisor ca-certificates \
    && mkdir -p /data /var/log/supervisor /usr/local/etc/teamagentica

COPY --from=builder /registry-plugin /usr/local/bin/registry-plugin
COPY plugins/infra-container-registry/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY plugins/infra-container-registry/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

EXPOSE 5000 8081
CMD ["supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
