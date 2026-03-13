# Grab sss3 binary from upstream image
FROM ghcr.io/espebra/stupid-simple-s3:latest AS sss3

# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3

WORKDIR /app

COPY . .

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /storage-sss3 .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
RUN mkdir -p /data/sss3 /data/sss3-tmp && chown -R appuser:appuser /data
COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3
COPY --from=builder /storage-sss3 /usr/local/bin/storage-sss3
USER appuser
CMD ["storage-sss3"]
