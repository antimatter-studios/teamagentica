# This Dockerfile should be built from the repo root:
# docker build -f plugins/discord/Dockerfile .
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/discord/ plugins/discord/
WORKDIR /app/plugins/discord
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /discord-plugin .

FROM alpine:3.19
RUN adduser -D -u 1000 appuser
COPY --from=builder /discord-plugin /usr/local/bin/discord-plugin
USER appuser
CMD ["discord-plugin"]
