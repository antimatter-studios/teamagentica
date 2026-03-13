# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git \
    && go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-agent-relay .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /infra-agent-relay /usr/local/bin/infra-agent-relay
USER appuser
EXPOSE 8081
CMD ["infra-agent-relay"]
