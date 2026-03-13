# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

EXPOSE 8081
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /infra-mcp-server .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /infra-mcp-server /usr/local/bin/infra-mcp-server

USER appuser
EXPOSE 8081
CMD ["infra-mcp-server"]
