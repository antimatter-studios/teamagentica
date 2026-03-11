# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache gcc musl-dev
RUN go install github.com/air-verse/air@latest

WORKDIR /app/plugins/user-claude-terminal

# Copy SDK and plugin sources
COPY pkg/pluginsdk/ /app/pkg/pluginsdk/
COPY plugins/user-claude-terminal/ /app/plugins/user-claude-terminal/

RUN go mod download

EXPOSE 8090
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY pkg/pluginsdk/ pkg/pluginsdk/
COPY plugins/user-claude-terminal/ plugins/user-claude-terminal/
WORKDIR /app/plugins/user-claude-terminal
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /user-claude-terminal .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /user-claude-terminal /usr/local/bin/user-claude-terminal

USER appuser
EXPOSE 8090
CMD ["user-claude-terminal"]
