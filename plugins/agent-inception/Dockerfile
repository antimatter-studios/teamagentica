# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-inception/ plugins/agent-inception/
WORKDIR /build/plugins/agent-inception

RUN go mod download

RUN mkdir -p /data

EXPOSE 8085
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-inception/ plugins/agent-inception/
WORKDIR /build/plugins/agent-inception
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-inception .

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /agent-inception /usr/local/bin/agent-inception
RUN mkdir -p /data && chown -R teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-inception/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8085
CMD ["agent-inception"]
