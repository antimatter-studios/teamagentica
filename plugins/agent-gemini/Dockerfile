# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-gemini/ plugins/agent-gemini/
WORKDIR /build/plugins/agent-gemini

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-gemini/ plugins/agent-gemini/
WORKDIR /build/plugins/agent-gemini
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /agent-gemini .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /agent-gemini /usr/local/bin/agent-gemini
RUN mkdir -p /data && chown teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-gemini/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["agent-gemini"]
