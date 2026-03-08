# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/network-ngrok

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/network-ngrok/ /app/plugins/network-ngrok/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/network-ngrok/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/network-ngrok/ plugins/network-ngrok/
WORKDIR /app/plugins/network-ngrok
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /teamagentica-network-ngrok .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /teamagentica-network-ngrok /usr/local/bin/teamagentica-network-ngrok
USER appuser
CMD ["teamagentica-network-ngrok"]
