# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/webhook-ingress

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/webhook-ingress/ /app/plugins/webhook-ingress/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/webhook-ingress/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/webhook-ingress/ plugins/webhook-ingress/
WORKDIR /app/plugins/webhook-ingress
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /teamagentica-webhook-ingress .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /teamagentica-webhook-ingress /usr/local/bin/teamagentica-webhook-ingress
USER appuser
EXPOSE 9000
CMD ["teamagentica-webhook-ingress"]
