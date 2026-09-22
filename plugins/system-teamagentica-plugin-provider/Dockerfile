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

# ── Manifest stage: the catalog this image ships with ──
#
# Every plugin's manifest is collected under its plugin id, so the provider can
# seed its catalog at boot. Without this the catalog starts empty and a fresh
# instance has nothing to install, since manifests only ever arrived by someone
# POSTing them to /manifests from a checkout.
#
# COPY cannot do this alone: `COPY plugins/*/plugin.yaml` flattens every file to
# the same name. Copying the manifests one directory at a time in a throwaway
# stage keeps the ids and leaves no plugin sources in the published image.
FROM alpine:3.19 AS manifests
COPY plugins/ /src/plugins/
RUN mkdir -p /manifests \
    && for d in /src/plugins/*/; do \
         [ -f "$d/plugin.yaml" ] && cp "$d/plugin.yaml" "/manifests/$(basename "$d").yaml"; \
       done; \
    ls /manifests | wc -l

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica \
    && mkdir -p /data && chown teamagentica:teamagentica /data

COPY --from=builder /system-teamagentica-plugin-provider /usr/local/bin/system-teamagentica-plugin-provider

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/system-teamagentica-plugin-provider/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
COPY --from=manifests /manifests/ /usr/local/share/teamagentica/manifests/
USER teamagentica
EXPOSE 8083
CMD ["system-teamagentica-plugin-provider"]
