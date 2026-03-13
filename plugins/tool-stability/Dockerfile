# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git
RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /stability-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /stability-plugin /usr/local/bin/stability-plugin
USER appuser
CMD ["stability-plugin"]
