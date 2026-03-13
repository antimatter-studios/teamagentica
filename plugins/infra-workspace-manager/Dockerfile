# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*

RUN apk add --no-cache git \
    && go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

EXPOSE 8091
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
ENV GOPRIVATE=github.com/antimatter-studios/*
ENV GONOSUMCHECK=github.com/antimatter-studios/*
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-workspace-manager .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN apk add --no-cache git \
    && adduser -D -u 1000 appuser
RUN mkdir -p /workspaces && chown -R appuser:appuser /workspaces
COPY --from=builder /infra-workspace-manager /usr/local/bin/infra-workspace-manager
USER appuser
EXPOSE 8091
CMD ["infra-workspace-manager"]
