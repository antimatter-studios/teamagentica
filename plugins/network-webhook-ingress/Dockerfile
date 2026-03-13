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
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o /teamagentica-network-webhook-ingress .

# -- Prod stage --
FROM alpine:3.19 AS prod
RUN adduser -D -u 1000 appuser
COPY --from=builder /teamagentica-network-webhook-ingress /usr/local/bin/teamagentica-network-webhook-ingress
USER appuser
EXPOSE 9000
CMD ["teamagentica-network-webhook-ingress"]
