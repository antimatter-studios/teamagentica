# Grab sss3 binary from upstream image
FROM ghcr.io/espebra/stupid-simple-s3:latest AS sss3

# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3

WORKDIR /app/plugins/sss3-storage

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/sss3-storage/ /app/plugins/sss3-storage/

RUN go mod download

CMD ["air", "-c", ".air.toml"]

# -- Build stage --
# Build from repo root: docker build -f plugins/sss3-storage/Dockerfile .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/sss3-storage/ plugins/sss3-storage/
WORKDIR /app/plugins/sss3-storage
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /sss3-storage-plugin .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
RUN mkdir -p /data/sss3 /data/sss3-tmp && chown -R appuser:appuser /data
COPY --from=sss3 /stupid-simple-s3 /usr/local/bin/stupid-simple-s3
COPY --from=builder /sss3-storage-plugin /usr/local/bin/sss3-storage-plugin
USER appuser
CMD ["sss3-storage-plugin"]
