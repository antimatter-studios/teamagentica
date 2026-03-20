# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-alias-registry/ plugins/infra-alias-registry/
WORKDIR /build/plugins/infra-alias-registry
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /infra-alias-registry .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /infra-alias-registry /usr/local/bin/infra-alias-registry
RUN mkdir -p /data && chown teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-alias-registry/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8090
CMD ["infra-alias-registry"]
