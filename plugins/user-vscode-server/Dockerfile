# -- Dev stage: hot reload with Air --
FROM golang:1.25-alpine AS dev

RUN apk add --no-cache git gcc musl-dev
RUN go install github.com/air-verse/air@latest

WORKDIR /app

COPY . .

RUN go mod download

EXPOSE 8090
CMD ["air", "-c", ".air.toml"]

# -- Build stage --
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git gcc musl-dev
WORKDIR /app
COPY . .
RUN go mod download && CGO_ENABLED=1 go build -ldflags="-s -w" -o /user-vscode-server .

# -- Prod stage --
FROM alpine:3.19 AS prod

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 appuser

COPY --from=builder /user-vscode-server /usr/local/bin/user-vscode-server

USER appuser
EXPOSE 8090
CMD ["user-vscode-server"]
