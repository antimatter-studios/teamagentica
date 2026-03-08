# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/network-webhook-ingress

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/network-webhook-ingress/ /app/plugins/network-webhook-ingress/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/network-webhook-ingress/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/network-webhook-ingress/ plugins/network-webhook-ingress/
WORKDIR /app/plugins/network-webhook-ingress
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /teamagentica-network-webhook-ingress .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /teamagentica-network-webhook-ingress /usr/local/bin/teamagentica-network-webhook-ingress
USER appuser
EXPOSE 9000
CMD ["teamagentica-network-webhook-ingress"]
