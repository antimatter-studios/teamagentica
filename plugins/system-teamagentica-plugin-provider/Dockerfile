# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
ENV CGO_ENABLED=1

RUN apk add --no-cache git gcc musl-dev
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/system-teamagentica-plugin-provider/ plugins/system-teamagentica-plugin-provider/
WORKDIR /build/plugins/system-teamagentica-plugin-provider

RUN go mod download

EXPOSE 8083
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/system-teamagentica-plugin-provider/ plugins/system-teamagentica-plugin-provider/
WORKDIR /build/plugins/system-teamagentica-plugin-provider
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /system-teamagentica-plugin-provider .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica \
    && mkdir -p /data && chown teamagentica:teamagentica /data

COPY --from=builder /system-teamagentica-plugin-provider /usr/local/bin/system-teamagentica-plugin-provider

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/system-teamagentica-plugin-provider/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8083
CMD ["system-teamagentica-plugin-provider"]
