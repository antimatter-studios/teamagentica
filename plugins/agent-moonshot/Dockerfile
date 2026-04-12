# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-moonshot/ plugins/agent-moonshot/
WORKDIR /build/plugins/agent-moonshot

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/agent-moonshot/ plugins/agent-moonshot/
WORKDIR /build/plugins/agent-moonshot
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /agent-moonshot .

# -- Kimi CLI installer --
FROM python:3.12-alpine AS kimi-installer
RUN pip install --no-cache-dir kimi-cli

# -- Prod stage --
FROM python:3.12-alpine AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

# Copy Python environment with kimi-cli installed.
COPY --from=kimi-installer /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY --from=kimi-installer /usr/local/bin/kimi /usr/local/bin/kimi

COPY --from=builder /agent-moonshot /usr/local/bin/agent-moonshot
RUN mkdir -p /data/kimi-home && chown -R teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/agent-moonshot/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8081
CMD ["agent-moonshot"]
