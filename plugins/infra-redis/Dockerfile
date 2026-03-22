# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-redis/ plugins/infra-redis/
WORKDIR /build/plugins/infra-redis

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-redis/ plugins/infra-redis/
WORKDIR /build/plugins/infra-redis
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-redis-plugin .

# -- Prod stage --
FROM redis:7-alpine AS prod
COPY --from=builder /infra-redis-plugin /usr/local/bin/infra-redis-plugin
RUN mkdir -p /data && mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-redis/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY plugins/infra-redis/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 6379 8081
CMD ["/entrypoint.sh"]
