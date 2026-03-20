# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
RUN apk add --no-cache git gcc musl-dev
WORKDIR /build
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/infra-agent-memory/ plugins/infra-agent-memory/
WORKDIR /build/plugins/infra-agent-memory
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /infra-agent-memory .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 teamagentica

COPY --from=builder /infra-agent-memory /usr/local/bin/infra-agent-memory
RUN mkdir -p /data && chown teamagentica:teamagentica /data

RUN mkdir -p /usr/local/etc/teamagentica
COPY plugins/infra-agent-memory/plugin.yaml /usr/local/etc/teamagentica/plugin.yaml
USER teamagentica
EXPOSE 8091
CMD ["infra-agent-memory"]
