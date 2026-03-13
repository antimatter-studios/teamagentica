# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

EXPOSE 8090
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /storage-volume .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
RUN mkdir -p /data && chown -R appuser:appuser /data
COPY --from=builder /storage-volume /usr/local/bin/storage-volume
USER appuser
EXPOSE 8090
CMD ["storage-volume"]
