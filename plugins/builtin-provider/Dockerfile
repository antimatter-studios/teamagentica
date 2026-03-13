# ── Dev stage: hot reload with Air ──
FROM golang:1.25-alpine AS dev

RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

# The catalog.yaml file is pre-built on the host before starting.
# It is bind-mounted into the container via the dev source mount.

RUN go mod download

EXPOSE 8083
CMD ["air", "-c", ".air.toml"]

# ── Build stage ──
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /builtin-provider .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /build-catalog ./cmd/build-catalog

# Generate the catalog from all plugin.yaml manifests.
RUN /build-catalog /app/plugins /catalog.yaml

# ── Prod stage ──
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /builtin-provider /usr/local/bin/builtin-provider
COPY --from=builder /catalog.yaml /app/plugins/builtin-provider/catalog.yaml

USER appuser
EXPOSE 8083
CMD ["builtin-provider"]
